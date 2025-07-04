package main

import (
	"fmt"
	"time"
	"strings"
)

// ========== 调试帧处理器 ==========

type FrameProcessor struct {
	ctx *DebuggerContext
}

// NewFrameProcessor 创建帧处理器
func NewFrameProcessor(ctx *DebuggerContext) *FrameProcessor {
	return &FrameProcessor{ctx: ctx}
}

// ========== BPF事件处理 ==========

// ProcessBPFEvent 处理BPF事件并创建调试帧
func (fp *FrameProcessor) ProcessBPFEvent(event *BPFDebugEvent) {
	if fp.ctx.CurrentSession == nil {
		sessionManager := NewSessionManager(fp.ctx)
		sessionManager.InitDebugSession()
	}
	
	// 创建新的调试帧
	frame := &DebugFrame{
		FrameID:   len(fp.ctx.CurrentSession.Frames) + 1,
		Timestamp: time.Now(),
		RawBPFEvent: event,
	}
	
	// 解析BPF事件数据
	frame.Registers = fp.ParseBPFRegisters(event)
	frame.LocalVariables = fp.ParseBPFLocalVariables(event)
	frame.GlobalVariables = fp.ParseBPFGlobalVariables(event)
	frame.StackData = make([]uint64, len(event.StackData))
	copy(frame.StackData, event.StackData[:])
	
	// 查找对应的断点信息
	frame.BreakpointInfo = fp.FindBreakpointByFunction(string(event.Function[:]))
	
	// 构建栈帧信息
	frame.StackFrames = fp.BuildStackFrames(event)
	
	// 构建调用链信息
	frame.CallChain = fp.BuildCallChain(event)
	
	// 添加到会话中
	fp.ctx.CurrentSession.Frames = append(fp.ctx.CurrentSession.Frames, *frame)
	fp.ctx.CurrentSession.CurrentFrameIndex = len(fp.ctx.CurrentSession.Frames) - 1
	
	// 设置为当前帧
	fp.ctx.CurrentFrame = frame
	
	// 更新会话信息
	fp.ctx.CurrentSession.SessionInfo.TotalFrames = len(fp.ctx.CurrentSession.Frames)
}

// ========== 寄存器数据解析 ==========

// ParseBPFRegisters 解析BPF寄存器数据
func (fp *FrameProcessor) ParseBPFRegisters(event *BPFDebugEvent) map[string]uint64 {
	return map[string]uint64{
		// RISC-V基础寄存器
		"PC": event.PC,   // 程序计数器
		"RA": event.RA,   // 返回地址
		"SP": event.SP,   // 栈指针
		"GP": event.GP,   // 全局指针
		"TP": event.TP,   // 线程指针
		
		// 临时寄存器
		"T0": event.T0,
		"T1": event.T1,
		"T2": event.T2,
		
		// 保存寄存器
		"S0": event.S0,   // 帧指针
		"S1": event.S1,
		
		// 参数/返回值寄存器
		"A0": event.A0,   // 第一个参数/返回值
		"A1": event.A1,   // 第二个参数
		"A2": event.A2,   // 第三个参数
		"A3": event.A3,   // 第四个参数
		"A4": event.A4,   // 第五个参数
		"A5": event.A5,   // 第六个参数
		"A6": event.A6,   // 第七个参数
		"A7": event.A7,   // 第八个参数
	}
}

// ========== 变量数据解析 ==========

// ParseBPFLocalVariables 解析BPF局部变量数据
func (fp *FrameProcessor) ParseBPFLocalVariables(event *BPFDebugEvent) map[string]interface{} {
	vars := make(map[string]interface{})
	
	// 将BPF捕获的局部变量数据转换为显示格式
	for i, value := range event.LocalVars {
		if value != 0 { // 只记录非零值
			vars[fmt.Sprintf("local_var_%d", i)] = fmt.Sprintf("0x%016x (%d)", value, value)
		}
	}
	
	// 添加函数参数（基于RISC-V调用约定）
	vars["arg0 (a0)"] = fmt.Sprintf("0x%016x (%d)", event.A0, event.A0)
	vars["arg1 (a1)"] = fmt.Sprintf("0x%016x (%d)", event.A1, event.A1)
	vars["arg2 (a2)"] = fmt.Sprintf("0x%016x (%d)", event.A2, event.A2)
	vars["arg3 (a3)"] = fmt.Sprintf("0x%016x (%d)", event.A3, event.A3)
	vars["arg4 (a4)"] = fmt.Sprintf("0x%016x (%d)", event.A4, event.A4)
	vars["arg5 (a5)"] = fmt.Sprintf("0x%016x (%d)", event.A5, event.A5)
	vars["arg6 (a6)"] = fmt.Sprintf("0x%016x (%d)", event.A6, event.A6)
	vars["arg7 (a7)"] = fmt.Sprintf("0x%016x (%d)", event.A7, event.A7)
	
	return vars
}

// ParseBPFGlobalVariables 解析全局变量（基于栈数据推测）
func (fp *FrameProcessor) ParseBPFGlobalVariables(event *BPFDebugEvent) map[string]interface{} {
	vars := make(map[string]interface{})
	
	// 基于栈数据和寄存器值推测全局变量
	// 这里可以根据具体的内核模块添加更多智能解析
	
	// 当前进程信息
	vars["current_pid"] = fmt.Sprintf("%d", event.PID)
	vars["current_tgid"] = fmt.Sprintf("%d", event.TGID)
	vars["timestamp"] = fmt.Sprintf("%d", event.Timestamp)
	
	// 全局指针相关
	if event.GP != 0 {
		vars["global_pointer"] = fmt.Sprintf("0x%016x", event.GP)
	}
	
	// 线程指针相关
	if event.TP != 0 {
		vars["thread_pointer"] = fmt.Sprintf("0x%016x", event.TP)
	}
	
	return vars
}

// ========== 栈帧构建 ==========

// BuildStackFrames 构建栈帧信息
func (fp *FrameProcessor) BuildStackFrames(event *BPFDebugEvent) []StackFrame {
	var frames []StackFrame
	
	// 当前函数帧
	functionName := strings.TrimRight(string(event.Function[:]), "\x00")
	if functionName == "" {
		functionName = "unknown_function"
	}
	
	currentFrame := StackFrame{
		FunctionName: functionName,
		FileName:     "unknown_file.c", // 实际实现中可以通过符号表获取
		LineNumber:   0,                // 实际实现中可以通过DWARF信息获取
		Address:      event.PC,
	}
	frames = append(frames, currentFrame)
	
	// 调用者帧（基于返回地址）
	if event.RA != 0 && event.RA != event.PC {
		callerFrame := StackFrame{
			FunctionName: "caller_function", // 实际实现中需要符号解析
			FileName:     "unknown_file.c",
			LineNumber:   0,
			Address:      event.RA,
		}
		frames = append(frames, callerFrame)
	}
	
	return frames
}

// BuildCallChain 构建调用链信息
func (fp *FrameProcessor) BuildCallChain(event *BPFDebugEvent) []CallFrame {
	functionName := strings.TrimRight(string(event.Function[:]), "\x00")
	if functionName == "" {
		functionName = "unknown_function"
	}
	
	return []CallFrame{
		{
			FunctionName:  functionName,
			ReturnAddress: event.RA,
			Arguments:     []uint64{event.A0, event.A1, event.A2, event.A3, event.A4, event.A5, event.A6, event.A7},
		},
	}
}

// ========== 断点匹配 ==========

// FindBreakpointByFunction 通过函数名查找断点
func (fp *FrameProcessor) FindBreakpointByFunction(functionName string) Breakpoint {
	if fp.ctx.Project == nil {
		return Breakpoint{}
	}
	
	// 清理函数名（移除null字符）
	functionName = strings.TrimRight(functionName, "\x00")
	
	for _, bp := range fp.ctx.Project.Breakpoints {
		if bp.Function == functionName {
			return bp
		}
	}
	
	// 如果没找到，返回默认断点
	return Breakpoint{
		Function: functionName,
		Enabled:  true,
		File:     "unknown_file.c",
		Line:     0,
	}
}

// ========== 帧数据分析 ==========

// AnalyzeFrameData 分析帧数据，提供调试洞察
func (fp *FrameProcessor) AnalyzeFrameData(frame *DebugFrame) map[string]string {
	analysis := make(map[string]string)
	
	if frame.RawBPFEvent == nil {
		analysis["error"] = "缺少原始BPF事件数据"
		return analysis
	}
	
	event := frame.RawBPFEvent
	
	// 分析栈指针
	if event.SP != 0 {
		analysis["stack_analysis"] = fmt.Sprintf("栈指针: 0x%016x", event.SP)
		
		// 检查栈是否在合理范围内
		if event.SP > 0x7fff00000000 && event.SP < 0x7fffffff0000 {
			analysis["stack_status"] = "✅ 栈指针在用户空间合理范围内"
		} else if event.SP > 0xffff800000000000 {
			analysis["stack_status"] = "✅ 栈指针在内核空间"
		} else {
			analysis["stack_status"] = "⚠️  栈指针可能异常"
		}
	}
	
	// 分析函数调用
	if event.RA != 0 && event.RA != event.PC {
		analysis["call_analysis"] = fmt.Sprintf("返回地址: 0x%016x", event.RA)
		analysis["call_status"] = "✅ 检测到函数调用"
	} else {
		analysis["call_status"] = "⚠️  未检测到明确的函数调用"
	}
	
	// 分析参数
	argCount := 0
	for _, arg := range []uint64{event.A0, event.A1, event.A2, event.A3, event.A4, event.A5, event.A6, event.A7} {
		if arg != 0 {
			argCount++
		}
	}
	analysis["args_analysis"] = fmt.Sprintf("非零参数数量: %d", argCount)
	
	// 分析栈数据
	nonZeroStack := 0
	for _, data := range event.StackData {
		if data != 0 {
			nonZeroStack++
		}
	}
	analysis["stack_data"] = fmt.Sprintf("栈中非零数据: %d/%d", nonZeroStack, len(event.StackData))
	
	return analysis
}

// CompareFrames 比较两个帧的差异
func (fp *FrameProcessor) CompareFrames(frame1, frame2 *DebugFrame) map[string]string {
	diff := make(map[string]string)
	
	if frame1.RawBPFEvent == nil || frame2.RawBPFEvent == nil {
		diff["error"] = "缺少BPF事件数据"
		return diff
	}
	
	event1, event2 := frame1.RawBPFEvent, frame2.RawBPFEvent
	
	// 比较寄存器变化
	regChanges := []string{}
	if event1.PC != event2.PC {
		regChanges = append(regChanges, fmt.Sprintf("PC: 0x%x → 0x%x", event1.PC, event2.PC))
	}
	if event1.SP != event2.SP {
		regChanges = append(regChanges, fmt.Sprintf("SP: 0x%x → 0x%x", event1.SP, event2.SP))
	}
	if event1.A0 != event2.A0 {
		regChanges = append(regChanges, fmt.Sprintf("A0: 0x%x → 0x%x", event1.A0, event2.A0))
	}
	
	if len(regChanges) > 0 {
		diff["register_changes"] = strings.Join(regChanges, ", ")
	} else {
		diff["register_changes"] = "无寄存器变化"
	}
	
	// 比较时间差
	timeDiff := frame2.Timestamp.Sub(frame1.Timestamp)
	diff["time_diff"] = fmt.Sprintf("时间间隔: %v", timeDiff)
	
	return diff
} 