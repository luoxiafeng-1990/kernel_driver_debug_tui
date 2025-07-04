package main

import (
	"fmt"
	"time"
	"math/rand"
	"context"
	"strings"
	
	"github.com/jroimartin/gocui"
)

// ========== BPF程序管理器 ==========

type BPFManager struct {
	ctx *DebuggerContext
}

// NewBPFManager 创建BPF管理器
func NewBPFManager(ctx *DebuggerContext) *BPFManager {
	return &BPFManager{ctx: ctx}
}

// ========== BPF程序启动和停止 ==========

// StartBPFProgram 启动BPF程序
func (bm *BPFManager) StartBPFProgram() error {
	if bm.ctx.BPFCtx != nil && bm.ctx.BPFCtx.Running {
		return fmt.Errorf("BPF程序已经在运行")
	}
	
	// 检查是否有断点设置
	if bm.ctx.Project == nil || len(bm.ctx.Project.Breakpoints) == 0 {
		return fmt.Errorf("请先设置断点")
	}
	
	// 创建BPF上下文
	ctx, cancel := context.WithCancel(context.Background())
	bm.ctx.BPFCtx = &BPFContext{
		ProgramFD: -1, // 模拟模式下设为-1
		Maps:      BPFMaps{EventsMap: -1, ControlMap: -1},
		Cancel:    cancel,
		Running:   true,
	}
	
	// 初始化数据通道
	bm.ctx.BPFDataChannel = make(chan *BPFDebugEvent, 100)
	
	// 启动数据接收协程
	go bm.runBPFDataReceiver(ctx)
	
	// 启动模拟数据生成协程（仅在模拟模式下）
	go bm.runMockDataGenerator(ctx)
	
	return nil
}

// StopBPFProgram 停止BPF程序
func (bm *BPFManager) StopBPFProgram() {
	if bm.ctx.BPFCtx == nil || !bm.ctx.BPFCtx.Running {
		return
	}
	
	// 停止BPF程序
	bm.ctx.BPFCtx.Running = false
	if bm.ctx.BPFCtx.Cancel != nil {
		bm.ctx.BPFCtx.Cancel()
	}
	
	// 关闭数据通道
	if bm.ctx.BPFDataChannel != nil {
		close(bm.ctx.BPFDataChannel)
		bm.ctx.BPFDataChannel = nil
	}
	
	bm.ctx.BPFCtx = nil
}

// ========== BPF数据接收 ==========

// runBPFDataReceiver BPF数据接收主循环
func (bm *BPFManager) runBPFDataReceiver(ctx context.Context) {
	frameProcessor := NewFrameProcessor(bm.ctx)
	
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-bm.ctx.BPFDataChannel:
			if !ok {
				return
			}
			
			// 处理BPF事件
			if bm.ctx.IsRecording {
				frameProcessor.ProcessBPFEvent(event)
				
				// 异步更新UI
				if bm.ctx.GUI != nil {
					bm.ctx.GUI.Update(func(g *gocui.Gui) error {
						return nil
					})
				}
			}
		}
	}
}

// ========== 模拟数据生成（用于测试） ==========

// runMockDataGenerator 运行模拟数据生成器
func (bm *BPFManager) runMockDataGenerator(ctx context.Context) {
	if bm.ctx.Project == nil || len(bm.ctx.Project.Breakpoints) == 0 {
		return
	}
	
	ticker := time.NewTicker(2 * time.Second) // 每2秒生成一个模拟事件
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if bm.ctx.IsRecording {
				event := bm.generateMockBPFEvent()
				select {
				case bm.ctx.BPFDataChannel <- event:
				default:
					// 通道满了，跳过这个事件
				}
			}
		}
	}
}

// generateMockBPFEvent 生成模拟BPF事件
func (bm *BPFManager) generateMockBPFEvent() *BPFDebugEvent {
	// 随机选择一个断点
	var selectedBreakpoint Breakpoint
	if len(bm.ctx.Project.Breakpoints) > 0 {
		selectedBreakpoint = bm.ctx.Project.Breakpoints[rand.Intn(len(bm.ctx.Project.Breakpoints))]
	}
	
	// 创建模拟事件
	event := &BPFDebugEvent{
		PID:          uint32(1000 + rand.Intn(1000)),
		TGID:         uint32(1000 + rand.Intn(1000)),
		Timestamp:    uint64(time.Now().UnixNano()),
		BreakpointID: uint32(rand.Intn(10)),
	}
	
	// 设置进程名
	copy(event.Comm[:], "test_process")
	
	// 设置函数名
	functionName := selectedBreakpoint.Function
	if functionName == "" {
		functionName = "mock_function"
	}
	copy(event.Function[:], functionName)
	
	// 生成模拟的RISC-V寄存器值
	baseAddr := uint64(0x7fff00000000 + rand.Intn(0x10000000))
	event.PC = baseAddr + uint64(rand.Intn(0x1000))
	event.RA = baseAddr + uint64(rand.Intn(0x1000))
	event.SP = 0x7fff80000000 + uint64(rand.Intn(0x1000000))
	event.GP = 0x10000000 + uint64(rand.Intn(0x1000000))
	event.TP = 0x7fff90000000 + uint64(rand.Intn(0x1000000))
	
	// 临时寄存器
	event.T0 = uint64(rand.Intn(0x10000))
	event.T1 = uint64(rand.Intn(0x10000))
	event.T2 = uint64(rand.Intn(0x10000))
	
	// 保存寄存器
	event.S0 = event.SP - uint64(rand.Intn(0x1000)) // 帧指针通常接近栈指针
	event.S1 = uint64(rand.Intn(0x10000))
	
	// 参数寄存器（模拟函数调用参数）
	event.A0 = uint64(rand.Intn(0x10000))
	event.A1 = uint64(rand.Intn(0x10000))
	event.A2 = uint64(rand.Intn(0x10000))
	event.A3 = uint64(rand.Intn(0x10000))
	event.A4 = uint64(rand.Intn(0x10000))
	event.A5 = uint64(rand.Intn(0x10000))
	event.A6 = uint64(rand.Intn(0x10000))
	event.A7 = uint64(rand.Intn(0x10000))
	
	// 生成模拟栈数据
	for i := range event.StackData {
		if rand.Float32() < 0.7 { // 70%概率有数据
			event.StackData[i] = uint64(rand.Intn(0x100000))
		}
	}
	
	// 生成模拟局部变量数据
	for i := range event.LocalVars {
		if rand.Float32() < 0.5 { // 50%概率有数据
			event.LocalVars[i] = uint64(rand.Intn(0x100000))
		}
	}
	
	return event
}

// ========== BPF程序状态查询 ==========

// IsRunning 检查BPF程序是否正在运行
func (bm *BPFManager) IsRunning() bool {
	return bm.ctx.BPFCtx != nil && bm.ctx.BPFCtx.Running
}

// GetBPFStatus 获取BPF程序状态信息
func (bm *BPFManager) GetBPFStatus() map[string]string {
	status := make(map[string]string)
	
	if bm.ctx.BPFCtx == nil {
		status["status"] = "未运行"
		status["program_fd"] = "N/A"
		status["events_map"] = "N/A"
		status["control_map"] = "N/A"
		return status
	}
	
	if bm.ctx.BPFCtx.Running {
		status["status"] = "运行中"
	} else {
		status["status"] = "已停止"
	}
	
	status["program_fd"] = fmt.Sprintf("%d", bm.ctx.BPFCtx.ProgramFD)
	status["events_map"] = fmt.Sprintf("%d", bm.ctx.BPFCtx.Maps.EventsMap)
	status["control_map"] = fmt.Sprintf("%d", bm.ctx.BPFCtx.Maps.ControlMap)
	
	// 数据通道状态
	if bm.ctx.BPFDataChannel != nil {
		status["data_channel"] = fmt.Sprintf("活跃 (缓冲: %d)", len(bm.ctx.BPFDataChannel))
	} else {
		status["data_channel"] = "未初始化"
	}
	
	return status
}

// ========== BPF程序调试支持 ==========

// ValidateBPFProgram 验证BPF程序配置
func (bm *BPFManager) ValidateBPFProgram() []string {
	var issues []string
	
	// 检查项目是否存在
	if bm.ctx.Project == nil {
		issues = append(issues, "❌ 项目未打开")
		return issues
	}
	
	// 检查断点配置
	if len(bm.ctx.Project.Breakpoints) == 0 {
		issues = append(issues, "❌ 未设置任何断点")
	} else {
		issues = append(issues, fmt.Sprintf("✅ 已设置 %d 个断点", len(bm.ctx.Project.Breakpoints)))
		
		// 检查断点有效性
		for i, bp := range bm.ctx.Project.Breakpoints {
			if bp.Function == "" {
				issues = append(issues, fmt.Sprintf("⚠️  断点 %d 缺少函数名", i+1))
			}
			if !bp.Enabled {
				issues = append(issues, fmt.Sprintf("⚠️  断点 %d 已禁用", i+1))
			}
		}
	}
	
	// 检查调试模式
	switch bm.ctx.DebugMode {
	case "live":
		issues = append(issues, "ℹ️  实时调试模式")
	case "recording":
		issues = append(issues, "🔴 录制模式")
	case "playback":
		issues = append(issues, "▶️  回放模式")
	default:
		issues = append(issues, "⚠️  未知调试模式")
	}
	
	// 检查BPF程序状态
	if bm.IsRunning() {
		issues = append(issues, "✅ BPF程序运行中")
	} else {
		issues = append(issues, "⚪ BPF程序未运行")
	}
	
	return issues
}

// GetBreakpointTargets 获取断点目标函数列表
func (bm *BPFManager) GetBreakpointTargets() []string {
	if bm.ctx.Project == nil {
		return []string{}
	}
	
	var targets []string
	for _, bp := range bm.ctx.Project.Breakpoints {
		if bp.Function != "" && bp.Enabled {
			targets = append(targets, bp.Function)
		}
	}
	
	return targets
}

// FormatBPFEventSummary 格式化BPF事件摘要
func (bm *BPFManager) FormatBPFEventSummary(event *BPFDebugEvent) string {
	if event == nil {
		return "无事件数据"
	}
	
	functionName := strings.TrimRight(string(event.Function[:]), "\x00")
	if functionName == "" {
		functionName = "unknown"
	}
	
	processName := strings.TrimRight(string(event.Comm[:]), "\x00")
	if processName == "" {
		processName = "unknown"
	}
	
	return fmt.Sprintf("[%s:%d] %s() - PC:0x%x SP:0x%x", 
		processName, event.PID, functionName, event.PC, event.SP)
}

// ========== BPF程序编译和生成 ==========

// CompileBPFProgram 编译BPF程序
func (bm *BPFManager) CompileBPFProgram(sourceCode string) error {
	if sourceCode == "" {
		return fmt.Errorf("BPF source code is empty")
	}
	
	// 模拟编译过程
	bm.ctx.CommandHistory = append(bm.ctx.CommandHistory, "Compiling BPF program...")
	bm.ctx.CommandDirty = true
	
	// 模拟编译延迟
	time.Sleep(100 * time.Millisecond)
	
	// 检查基本语法
	if !strings.Contains(sourceCode, "SEC(") {
		return fmt.Errorf("BPF program must contain SEC() macro")
	}
	
	if !strings.Contains(sourceCode, "bpf_") {
		return fmt.Errorf("BPF program should use BPF helper functions")
	}
	
	bm.ctx.CommandHistory = append(bm.ctx.CommandHistory, "BPF program compiled successfully")
	bm.ctx.CommandDirty = true
	
	return nil
}

// GenerateBPFProgram 生成BPF程序
func (bm *BPFManager) GenerateBPFProgram() (string, error) {
	if bm.ctx.Project == nil || len(bm.ctx.Project.Breakpoints) == 0 {
		return "", fmt.Errorf("no breakpoints set")
	}
	
	generator := NewBPFCodeGenerator(bm.ctx)
	return generator.GenerateBPFProgram()
}

// LoadBPFProgram 加载BPF程序
func (bm *BPFManager) LoadBPFProgram(programPath string) error {
	bm.ctx.CommandHistory = append(bm.ctx.CommandHistory, 
		fmt.Sprintf("Loading BPF program from: %s", programPath))
	bm.ctx.CommandDirty = true
	
	// 模拟加载过程
	time.Sleep(200 * time.Millisecond)
	
	// 在实际实现中，这里会:
	// 1. 读取编译好的BPF字节码
	// 2. 使用bpf系统调用加载程序
	// 3. 创建BPF映射
	// 4. 附加到内核事件
	
	bm.ctx.BpfLoaded = true
	bm.ctx.CommandHistory = append(bm.ctx.CommandHistory, "BPF program loaded successfully")
	bm.ctx.CommandDirty = true
	
	return nil
}

// UnloadBPFProgram 卸载BPF程序
func (bm *BPFManager) UnloadBPFProgram() error {
	if !bm.ctx.BpfLoaded {
		return fmt.Errorf("no BPF program loaded")
	}
	
	// 停止BPF程序
	bm.StopBPFProgram()
	
	// 清理资源
	bm.ctx.BpfLoaded = false
	
	bm.ctx.CommandHistory = append(bm.ctx.CommandHistory, "BPF program unloaded")
	bm.ctx.CommandDirty = true
	
	return nil
}

// ========== BPF程序状态管理 ==========

// GetBPFProgramInfo 获取BPF程序信息
func (bm *BPFManager) GetBPFProgramInfo() map[string]interface{} {
	info := make(map[string]interface{})
	
	info["loaded"] = bm.ctx.BpfLoaded
	info["running"] = bm.IsRunning()
	
	if bm.ctx.BPFCtx != nil {
		info["program_fd"] = bm.ctx.BPFCtx.ProgramFD
		info["events_map_fd"] = bm.ctx.BPFCtx.Maps.EventsMap
		info["control_map_fd"] = bm.ctx.BPFCtx.Maps.ControlMap
	}
	
	if bm.ctx.Project != nil {
		info["breakpoints_count"] = len(bm.ctx.Project.Breakpoints)
	}
	
	// 运行时统计
	if bm.ctx.RuntimeStats != nil {
		info["events_received"] = bm.ctx.RuntimeStats.EventsReceived
		info["frames_processed"] = bm.ctx.RuntimeStats.FramesProcessed
		info["uptime"] = time.Since(bm.ctx.RuntimeStats.StartTime).String()
	}
	
	return info
}

// ResetBPFProgram 重置BPF程序
func (bm *BPFManager) ResetBPFProgram() error {
	// 停止当前程序
	if bm.IsRunning() {
		bm.StopBPFProgram()
	}
	
	// 卸载程序
	if bm.ctx.BpfLoaded {
		bm.UnloadBPFProgram()
	}
	
	// 清理状态
	bm.ctx.CurrentFrame = nil
	bm.ctx.RuntimeStats = nil
	
	bm.ctx.CommandHistory = append(bm.ctx.CommandHistory, "BPF program reset")
	bm.ctx.CommandDirty = true
	
	return nil
}

// ========== BPF事件过滤 ==========

// SetEventFilter 设置事件过滤器
func (bm *BPFManager) SetEventFilter(filter map[string]interface{}) error {
	if !bm.ctx.BpfLoaded {
		return fmt.Errorf("no BPF program loaded")
	}
	
	// 将过滤器保存到上下文
	if bm.ctx.BPFEventFilter == nil {
		bm.ctx.BPFEventFilter = make(map[string]interface{})
	}
	
	for key, value := range filter {
		bm.ctx.BPFEventFilter[key] = value
	}
	
	bm.ctx.CommandHistory = append(bm.ctx.CommandHistory, 
		fmt.Sprintf("Updated BPF event filter: %v", filter))
	bm.ctx.CommandDirty = true
	
	return nil
}

// ClearEventFilter 清除事件过滤器
func (bm *BPFManager) ClearEventFilter() {
	bm.ctx.BPFEventFilter = nil
	
	bm.ctx.CommandHistory = append(bm.ctx.CommandHistory, "Cleared BPF event filter")
	bm.ctx.CommandDirty = true
}

// FilterEvent 过滤事件
func (bm *BPFManager) FilterEvent(event *BPFDebugEvent) bool {
	if bm.ctx.BPFEventFilter == nil {
		return true // 没有过滤器，接受所有事件
	}
	
	// PID过滤
	if pidFilter, exists := bm.ctx.BPFEventFilter["pid"]; exists {
		if pid, ok := pidFilter.(uint32); ok && event.PID != pid {
			return false
		}
	}
	
	// 函数名过滤
	if funcFilter, exists := bm.ctx.BPFEventFilter["function"]; exists {
		if funcName, ok := funcFilter.(string); ok {
			eventFunc := string(event.Function[:])
			if !strings.Contains(eventFunc, funcName) {
				return false
			}
		}
	}
	
	// 地址范围过滤
	if addrMinFilter, exists := bm.ctx.BPFEventFilter["addr_min"]; exists {
		if addrMin, ok := addrMinFilter.(uint64); ok && event.PC < addrMin {
			return false
		}
	}
	
	if addrMaxFilter, exists := bm.ctx.BPFEventFilter["addr_max"]; exists {
		if addrMax, ok := addrMaxFilter.(uint64); ok && event.PC > addrMax {
			return false
		}
	}
	
	return true
}