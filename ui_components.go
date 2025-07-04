package main

import (
	"fmt"
	"strings"
	"strconv"
	
	"github.com/jroimartin/gocui"
)

// ========== UI组件管理器 ==========

type UIManager struct {
	ctx *DebuggerContext
	gui *gocui.Gui
}

// NewUIManager 创建UI管理器
func NewUIManager(ctx *DebuggerContext, gui *gocui.Gui) *UIManager {
	return &UIManager{ctx: ctx, gui: gui}
}

// ========== 视图更新函数 ==========

// UpdateFileListView 更新文件列表视图
func (ui *UIManager) UpdateFileListView(v *gocui.View) error {
	v.Clear()
	
	if ui.ctx.Project == nil || ui.ctx.Project.FileTree == nil {
		fmt.Fprintln(v, "没有打开的项目")
		return nil
	}
	
	// 渲染文件树
	ui.renderFileTree(v, ui.ctx.Project.FileTree, 0)
	
	return nil
}

// renderFileTree 递归渲染文件树
func (ui *UIManager) renderFileTree(v *gocui.View, node *FileNode, depth int) {
	if node == nil {
		return
	}
	
	indent := strings.Repeat("  ", depth)
	
	if node.IsDir {
		if node.Expanded {
			fmt.Fprintf(v, "%s📁 %s\n", indent, node.Name)
			if node.Children != nil {
				for _, child := range node.Children {
					ui.renderFileTree(v, child, depth+1)
				}
			}
		} else {
			fmt.Fprintf(v, "%s📂 %s\n", indent, node.Name)
		}
	} else {
		// 文件图标根据扩展名选择
		icon := ui.getFileIcon(node.Name)
		fmt.Fprintf(v, "%s%s %s\n", indent, icon, node.Name)
	}
}

// getFileIcon 根据文件类型获取图标
func (ui *UIManager) getFileIcon(filename string) string {
	if strings.HasSuffix(filename, ".c") || strings.HasSuffix(filename, ".h") {
		return "📄"
	} else if strings.HasSuffix(filename, ".go") {
		return "🐹"
	} else if strings.HasSuffix(filename, ".py") {
		return "🐍"
	} else if strings.HasSuffix(filename, ".js") {
		return "📜"
	} else if strings.HasSuffix(filename, ".md") {
		return "📝"
	} else if strings.HasSuffix(filename, ".txt") {
		return "📃"
	}
	return "📄"
}

// UpdateCodeView 更新代码视图
func (ui *UIManager) UpdateCodeView(v *gocui.View) error {
	v.Clear()
	
	if ui.ctx.Project == nil || ui.ctx.Project.CurrentFile == "" {
		fmt.Fprintln(v, "请选择一个文件来查看代码")
		return nil
	}
	
	fileManager := NewFileManager(ui.ctx)
	content, err := fileManager.GetCurrentFileContent()
	if err != nil {
		fmt.Fprintf(v, "读取文件失败: %v\n", err)
		return nil
	}
	
	// 显示文件名
	fmt.Fprintf(v, "文件: %s\n", ui.ctx.Project.CurrentFile)
	fmt.Fprintln(v, strings.Repeat("-", 60))
	
	// 显示代码内容并加上行号
	for i, line := range content {
		lineNum := i + 1
		
		// 检查是否有断点
		hasBreakpoint := fileManager.HasBreakpoint(ui.ctx.Project.CurrentFile, lineNum)
		
		// 格式化行号和内容
		if hasBreakpoint {
			fmt.Fprintf(v, "🔴 %4d: %s\n", lineNum, line)
		} else {
			fmt.Fprintf(v, "   %4d: %s\n", lineNum, line)
		}
	}
	
	return nil
}

// UpdateRegistersView 更新寄存器视图
func (ui *UIManager) UpdateRegistersView(v *gocui.View) error {
	v.Clear()
	
	fmt.Fprintln(v, "寄存器状态")
	fmt.Fprintln(v, strings.Repeat("-", 30))
	
	if ui.ctx.CurrentFrame == nil {
		// 显示默认/模拟寄存器状态
		fmt.Fprintln(v, "PC : 0x0000000000000000")
		fmt.Fprintln(v, "RA : 0x0000000000000000")
		fmt.Fprintln(v, "SP : 0x0000000000000000")
		fmt.Fprintln(v, "GP : 0x0000000000000000")
		fmt.Fprintln(v, "TP : 0x0000000000000000")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "T0 : 0x0000000000000000")
		fmt.Fprintln(v, "T1 : 0x0000000000000000")
		fmt.Fprintln(v, "T2 : 0x0000000000000000")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "S0 : 0x0000000000000000")
		fmt.Fprintln(v, "S1 : 0x0000000000000000")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "A0 : 0x0000000000000000")
		fmt.Fprintln(v, "A1 : 0x0000000000000000")
		fmt.Fprintln(v, "A2 : 0x0000000000000000")
		fmt.Fprintln(v, "A3 : 0x0000000000000000")
		fmt.Fprintln(v, "A4 : 0x0000000000000000")
		fmt.Fprintln(v, "A5 : 0x0000000000000000")
		fmt.Fprintln(v, "A6 : 0x0000000000000000")
		fmt.Fprintln(v, "A7 : 0x0000000000000000")
		return nil
	}
	
	// 显示当前帧的寄存器状态
	regs := ui.ctx.CurrentFrame.Registers
	
	// RISC-V基础寄存器
	fmt.Fprintf(v, "PC : 0x%016x\n", regs["PC"])
	fmt.Fprintf(v, "RA : 0x%016x\n", regs["RA"])
	fmt.Fprintf(v, "SP : 0x%016x\n", regs["SP"])
	fmt.Fprintf(v, "GP : 0x%016x\n", regs["GP"])
	fmt.Fprintf(v, "TP : 0x%016x\n", regs["TP"])
	fmt.Fprintln(v, "")
	
	// 临时寄存器
	fmt.Fprintf(v, "T0 : 0x%016x\n", regs["T0"])
	fmt.Fprintf(v, "T1 : 0x%016x\n", regs["T1"])
	fmt.Fprintf(v, "T2 : 0x%016x\n", regs["T2"])
	fmt.Fprintln(v, "")
	
	// 保存寄存器
	fmt.Fprintf(v, "S0 : 0x%016x\n", regs["S0"])
	fmt.Fprintf(v, "S1 : 0x%016x\n", regs["S1"])
	fmt.Fprintln(v, "")
	
	// 参数/返回值寄存器
	fmt.Fprintf(v, "A0 : 0x%016x\n", regs["A0"])
	fmt.Fprintf(v, "A1 : 0x%016x\n", regs["A1"])
	fmt.Fprintf(v, "A2 : 0x%016x\n", regs["A2"])
	fmt.Fprintf(v, "A3 : 0x%016x\n", regs["A3"])
	fmt.Fprintf(v, "A4 : 0x%016x\n", regs["A4"])
	fmt.Fprintf(v, "A5 : 0x%016x\n", regs["A5"])
	fmt.Fprintf(v, "A6 : 0x%016x\n", regs["A6"])
	fmt.Fprintf(v, "A7 : 0x%016x\n", regs["A7"])
	
	return nil
}

// UpdateVariablesView 更新变量视图
func (ui *UIManager) UpdateVariablesView(v *gocui.View) error {
	v.Clear()
	
	fmt.Fprintln(v, "变量")
	fmt.Fprintln(v, strings.Repeat("-", 30))
	
	if ui.ctx.CurrentFrame == nil {
		fmt.Fprintln(v, "没有当前帧数据")
		return nil
	}
	
	// 显示局部变量
	fmt.Fprintln(v, "局部变量:")
	if len(ui.ctx.CurrentFrame.LocalVariables) > 0 {
		for name, value := range ui.ctx.CurrentFrame.LocalVariables {
			fmt.Fprintf(v, "  %s = %v\n", name, value)
		}
	} else {
		fmt.Fprintln(v, "  无")
	}
	
	fmt.Fprintln(v, "")
	
	// 显示全局变量
	fmt.Fprintln(v, "全局变量:")
	if len(ui.ctx.CurrentFrame.GlobalVariables) > 0 {
		for name, value := range ui.ctx.CurrentFrame.GlobalVariables {
			fmt.Fprintf(v, "  %s = %v\n", name, value)
		}
	} else {
		fmt.Fprintln(v, "  无")
	}
	
	return nil
}

// UpdateStackView 更新堆栈视图
func (ui *UIManager) UpdateStackView(v *gocui.View) error {
	v.Clear()
	
	fmt.Fprintln(v, "堆栈")
	fmt.Fprintln(v, strings.Repeat("-", 30))
	
	if ui.ctx.CurrentFrame == nil {
		fmt.Fprintln(v, "没有当前帧数据")
		return nil
	}
	
	// 显示调用链
	fmt.Fprintln(v, "调用链:")
	if len(ui.ctx.CurrentFrame.CallChain) > 0 {
		for i, call := range ui.ctx.CurrentFrame.CallChain {
			fmt.Fprintf(v, "  %d. %s()\n", i+1, call.FunctionName)
			fmt.Fprintf(v, "     返回地址: 0x%016x\n", call.ReturnAddress)
		}
	} else {
		fmt.Fprintln(v, "  无")
	}
	
	fmt.Fprintln(v, "")
	
	// 显示栈帧
	fmt.Fprintln(v, "栈帧:")
	if len(ui.ctx.CurrentFrame.StackFrames) > 0 {
		for i, frame := range ui.ctx.CurrentFrame.StackFrames {
			fmt.Fprintf(v, "  %d. %s @ 0x%016x\n", i+1, frame.FunctionName, frame.Address)
			if frame.FileName != "" {
				fmt.Fprintf(v, "     文件: %s:%d\n", frame.FileName, frame.LineNumber)
			}
		}
	} else {
		fmt.Fprintln(v, "  无")
	}
	
	fmt.Fprintln(v, "")
	
	// 显示栈数据
	fmt.Fprintln(v, "栈数据:")
	if len(ui.ctx.CurrentFrame.StackData) > 0 {
		for i, data := range ui.ctx.CurrentFrame.StackData {
			if data != 0 {
				fmt.Fprintf(v, "  [%d] 0x%016x (%d)\n", i, data, data)
			}
		}
	} else {
		fmt.Fprintln(v, "  无")
	}
	
	return nil
}

// UpdateStatusView 更新状态栏
func (ui *UIManager) UpdateStatusView(v *gocui.View) error {
	v.Clear()
	
	var statusParts []string
	
	// 调试模式状态
	switch ui.ctx.DebugMode {
	case "live":
		statusParts = append(statusParts, "🟢 实时")
	case "recording":
		statusParts = append(statusParts, "🔴 录制中")
	case "playback":
		statusParts = append(statusParts, "▶️  回放")
	default:
		statusParts = append(statusParts, "⚪ 未知")
	}
	
	// BPF程序状态
	bpfManager := NewBPFManager(ui.ctx)
	if bpfManager.IsRunning() {
		statusParts = append(statusParts, "BPF: 运行中")
	} else {
		statusParts = append(statusParts, "BPF: 停止")
	}
	
	// 断点信息
	if ui.ctx.Project != nil {
		statusParts = append(statusParts, fmt.Sprintf("断点: %d", len(ui.ctx.Project.Breakpoints)))
	}
	
	// 帧信息
	sessionManager := NewSessionManager(ui.ctx)
	frameInfo := sessionManager.GetCurrentFrameInfo()
	if frameInfo != "没有可用的调试会话" {
		statusParts = append(statusParts, frameInfo)
	}
	
	// 快捷键提示
	statusParts = append(statusParts, "F9:上一帧 F10:下一帧 F1:帮助")
	
	fmt.Fprint(v, strings.Join(statusParts, " | "))
	
	return nil
}

// UpdateCommandView 更新命令窗口
func (ui *UIManager) UpdateCommandView(v *gocui.View) error {
	if !ui.ctx.CommandDirty {
		return nil
	}
	
	v.Clear()
	
	// 显示命令历史（最后几行）
	maxLines := 20 // 显示最后20行
	startIndex := 0
	if len(ui.ctx.CommandHistory) > maxLines {
		startIndex = len(ui.ctx.CommandHistory) - maxLines
	}
	
	for i := startIndex; i < len(ui.ctx.CommandHistory); i++ {
		fmt.Fprintln(v, ui.ctx.CommandHistory[i])
	}
	
	// 显示当前输入
	if ui.ctx.CurrentInput != "" {
		fmt.Fprintf(v, "> %s", ui.ctx.CurrentInput)
	} else {
		fmt.Fprint(v, "> ")
	}
	
	ui.ctx.CommandDirty = false
	return nil
}

// ========== 帮助和信息显示 ==========

// ShowHelp 显示帮助信息
func (ui *UIManager) ShowHelp() []string {
	return []string{
		"RISC-V内核调试器 TUI - 帮助",
		"",
		"=== 基本命令 ===",
		"open <path>              - 打开项目目录",
		"file <path>              - 打开文件",
		"quit / exit              - 退出程序",
		"clear                    - 清除命令历史",
		"",
		"=== 断点管理 ===",
		"breakpoint add <func>    - 添加函数断点",
		"breakpoint add <line>    - 在当前文件指定行添加断点",
		"breakpoint list          - 列出所有断点",
		"breakpoint remove <id>   - 移除断点",
		"breakpoint toggle <id>   - 切换断点启用状态",
		"",
		"=== BPF程序 ===",
		"bpf generate             - 生成BPF程序",
		"bpf compile              - 编译BPF程序",
		"bpf status               - 查看BPF程序状态",
		"",
		"=== 录制回放系统 ===",
		"start-recording          - 开始录制调试会话",
		"stop-recording           - 停止录制并保存",
		"load-session <file>      - 加载会话文件进行回放",
		"save-session <file>      - 保存当前会话",
		"list-sessions            - 列出可用的会话文件",
		"",
		"=== 帧导航 ===",
		"jump-frame <index>       - 跳转到指定帧",
		"next-frame               - 下一帧",
		"prev-frame               - 上一帧",
		"frame-info               - 显示当前帧信息",
		"show-timeline            - 切换时间线视图",
		"",
		"=== 搜索功能 ===",
		"search <term>            - 在当前文件中搜索",
		"find <term>              - 搜索的别名",
		"next-match / n           - 下一个匹配项",
		"prev-match / p           - 上一个匹配项",
		"",
		"=== 时间旅行调试 ===",
		"F9                       - 上一帧（时间旅行）",
		"F10                      - 下一帧（时间旅行）", 
		"",
		"=== 工作流程 ===",
		"1. 打开项目: open /path/to/kernel/module",
		"2. 设置断点: breakpoint add function_name",
		"3. 生成BPF: bpf generate",
		"4. 开始录制: start-recording",
		"5. 触发调试事件...",
		"6. 停止录制: stop-recording",
		"7. 回放分析: load-session file.frames",
		"8. 时间旅行: F9/F10浏览帧",
		"",
		"=== 界面操作 ===",
		"Tab              - 在窗口间切换焦点",
		"Enter            - 在文件浏览器中打开文件/目录",
		"Space            - 在文件浏览器中展开/收缩目录",
		"Ctrl+F           - 开始搜索",
		"ESC              - 关闭弹出窗口/退出全屏/退出搜索",
		"Mouse            - 支持鼠标点击选择",
		"F1               - 显示此帮助",
		"Ctrl+C           - 退出程序",
		"",
		"=== 动态窗口调整 ===",
		"Ctrl+J           - 增加命令窗口高度",
		"Ctrl+K           - 减少命令窗口高度",
		"Ctrl+H           - 减少左侧面板宽度（代码区域变大）",
		"Ctrl+L           - 增加左侧面板宽度（代码区域变小）",
		"",
		"=== 命令行使用 ===",
		"./debug-gocui [project_path]  - 启动并打开指定项目",
	}
}

// ========== 命令执行 ==========

// ExecuteCommand 执行用户命令
func (ui *UIManager) ExecuteCommand(command string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return
	}
	
	// 添加命令到历史
	ui.ctx.CommandHistory = append(ui.ctx.CommandHistory, "> "+command)
	
	// 解析命令
	parts := strings.Fields(command)
	cmd := parts[0]
	args := parts[1:]
	
	var output string
	
	switch cmd {
	case "help":
		helpLines := ui.ShowHelp()
		output = strings.Join(helpLines, "\n")
		
	case "open":
		output = ui.executeOpenCommand(args)
		
	case "file":
		output = ui.executeFileCommand(args)
		
	case "breakpoint", "bp":
		output = ui.executeBreakpointCommand(args)
		
	case "bpf":
		output = ui.executeBPFCommand(args)
		
	case "start-recording":
		output = ui.executeStartRecordingCommand()
		
	case "stop-recording":
		output = ui.executeStopRecordingCommand()
		
	case "load-session":
		output = ui.executeLoadSessionCommand(args)
		
	case "save-session":
		output = ui.executeSaveSessionCommand(args)
		
	case "list-sessions":
		output = ui.executeListSessionsCommand()
		
	case "jump-frame":
		output = ui.executeJumpFrameCommand(args)
		
	case "next-frame":
		output = ui.executeNextFrameCommand()
		
	case "prev-frame":
		output = ui.executePrevFrameCommand()
		
	case "frame-info":
		output = ui.executeFrameInfoCommand()
		
	case "show-timeline":
		output = ui.executeShowTimelineCommand()
		
	case "search", "find":
		output = ui.executeSearchCommand(args)
		
	case "next-match", "n":
		ui.NextSearchResult()
		return
		
	case "prev-match", "p":
		ui.PrevSearchResult()
		return
		
	case "clear":
		ui.ctx.CommandHistory = []string{}
		output = "命令历史已清除"
		
	case "quit", "exit":
		ui.gui.Update(func(g *gocui.Gui) error {
			return gocui.ErrQuit
		})
		return
		
	default:
		output = fmt.Sprintf("未知命令: %s (输入 help 查看帮助)", cmd)
	}
	
	// 添加输出到历史
	if output != "" {
		ui.ctx.CommandHistory = append(ui.ctx.CommandHistory, output)
	}
	
	// 标记需要重绘
	ui.ctx.CommandDirty = true
}

// ========== 命令执行辅助函数 ==========

func (ui *UIManager) executeOpenCommand(args []string) string {
	if len(args) == 0 {
		return "用法: open <目录路径>"
	}
	
	fileManager := NewFileManager(ui.ctx)
	err := fileManager.InitProject(args[0])
	if err != nil {
		return fmt.Sprintf("打开项目失败: %v", err)
	}
	
	return fmt.Sprintf("已打开项目: %s", args[0])
}

func (ui *UIManager) executeFileCommand(args []string) string {
	if len(args) == 0 {
		return "用法: file <文件路径>"
	}
	
	fileManager := NewFileManager(ui.ctx)
	err := fileManager.OpenFile(args[0])
	if err != nil {
		return fmt.Sprintf("打开文件失败: %v", err)
	}
	
	return fmt.Sprintf("已打开文件: %s", args[0])
}

func (ui *UIManager) executeBreakpointCommand(args []string) string {
	if len(args) == 0 {
		return "用法: breakpoint <add|list|remove|toggle> [参数]"
	}
	
	fileManager := NewFileManager(ui.ctx)
	subCmd := args[0]
	
	switch subCmd {
	case "add":
		if len(args) < 2 {
			return "用法: breakpoint add <函数名|行号>"
		}
		
		// 尝试解析为行号
		if lineNum, err := strconv.Atoi(args[1]); err == nil {
			err := fileManager.AddBreakpointAtLine(args[1])
			if err != nil {
				return fmt.Sprintf("添加断点失败: %v", err)
			}
			return fmt.Sprintf("已在第 %d 行添加断点", lineNum)
		} else {
			// 作为函数名处理
			err := fileManager.AddBreakpointByFunction(args[1])
			if err != nil {
				return fmt.Sprintf("添加断点失败: %v", err)
			}
			return fmt.Sprintf("已为函数 %s 添加断点", args[1])
		}
		
	case "list":
		breakpoints := fileManager.GetBreakpoints()
		if len(breakpoints) == 0 {
			return "没有设置断点"
		}
		
		var lines []string
		lines = append(lines, "断点列表:")
		for i, bp := range breakpoints {
			status := "启用"
			if !bp.Enabled {
				status = "禁用"
			}
			lines = append(lines, fmt.Sprintf("  %d. %s:%d - %s (%s)", 
				i+1, bp.File, bp.Line, bp.Function, status))
		}
		return strings.Join(lines, "\n")
		
	default:
		return "用法: breakpoint <add|list|remove|toggle> [参数]"
	}
}

func (ui *UIManager) executeBPFCommand(args []string) string {
	if len(args) == 0 {
		return "用法: bpf <generate|compile|status>"
	}
	
	subCmd := args[0]
	
	switch subCmd {
	case "generate":
		generator := NewBPFCodeGenerator(ui.ctx)
		err := generator.GenerateAndSaveBPFProgram()
		if err != nil {
			return fmt.Sprintf("生成BPF程序失败: %v", err)
		}
		return "BPF程序已生成: generated_debug.bpf.c"
		
	case "compile":
		return "编译功能: 请运行 'make bpf-only' 编译BPF程序"
		
	case "status":
		bpfManager := NewBPFManager(ui.ctx)
		status := bpfManager.GetBPFStatus()
		
		var lines []string
		lines = append(lines, "BPF程序状态:")
		for key, value := range status {
			lines = append(lines, fmt.Sprintf("  %s: %s", key, value))
		}
		
		return strings.Join(lines, "\n")
		
	default:
		return "用法: bpf <generate|compile|status>"
	}
}

func (ui *UIManager) executeStartRecordingCommand() string {
	sessionManager := NewSessionManager(ui.ctx)
	err := sessionManager.StartRecording()
	if err != nil {
		return fmt.Sprintf("开始录制失败: %v", err)
	}
	return "已开始录制调试会话"
}

func (ui *UIManager) executeStopRecordingCommand() string {
	sessionManager := NewSessionManager(ui.ctx)
	err := sessionManager.StopRecording()
	if err != nil {
		return fmt.Sprintf("停止录制失败: %v", err)
	}
	return "录制已停止并保存"
}

func (ui *UIManager) executeLoadSessionCommand(args []string) string {
	if len(args) == 0 {
		return "用法: load-session <文件名>"
	}
	
	sessionManager := NewSessionManager(ui.ctx)
	err := sessionManager.LoadDebugSession(args[0])
	if err != nil {
		return fmt.Sprintf("加载会话失败: %v", err)
	}
	return fmt.Sprintf("已加载调试会话: %s", args[0])
}

func (ui *UIManager) executeSaveSessionCommand(args []string) string {
	if len(args) == 0 {
		return "用法: save-session <文件名>"
	}
	
	sessionManager := NewSessionManager(ui.ctx)
	err := sessionManager.SaveDebugSession(args[0])
	if err != nil {
		return fmt.Sprintf("保存会话失败: %v", err)
	}
	return fmt.Sprintf("已保存调试会话: %s", args[0])
}

func (ui *UIManager) executeListSessionsCommand() string {
	sessionManager := NewSessionManager(ui.ctx)
	sessions, err := sessionManager.ListDebugSessions()
	if err != nil {
		return fmt.Sprintf("列出会话失败: %v", err)
	}
	
	if len(sessions) == 0 {
		return "没有可用的调试会话文件"
	}
	
	var lines []string
	lines = append(lines, "可用的调试会话文件 (按时间倒序):")
	for i, session := range sessions {
		lines = append(lines, fmt.Sprintf("  %d. %s", i+1, session))
	}
	
	return strings.Join(lines, "\n")
}

func (ui *UIManager) executeJumpFrameCommand(args []string) string {
	if len(args) == 0 {
		return "用法: jump-frame <帧索引>"
	}
	
	frameIndex, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Sprintf("无效的帧索引: %s", args[0])
	}
	
	sessionManager := NewSessionManager(ui.ctx)
	err = sessionManager.JumpToFrame(frameIndex - 1) // 用户输入从1开始，内部从0开始
	if err != nil {
		return fmt.Sprintf("跳转失败: %v", err)
	}
	
	return fmt.Sprintf("已跳转到帧 %d", frameIndex)
}

func (ui *UIManager) executeNextFrameCommand() string {
	sessionManager := NewSessionManager(ui.ctx)
	err := sessionManager.NextFrame()
	if err != nil {
		return fmt.Sprintf("下一帧失败: %v", err)
	}
	return "已跳转到下一帧"
}

func (ui *UIManager) executePrevFrameCommand() string {
	sessionManager := NewSessionManager(ui.ctx)
	err := sessionManager.PrevFrame()
	if err != nil {
		return fmt.Sprintf("上一帧失败: %v", err)
	}
	return "已跳转到上一帧"
}

func (ui *UIManager) executeFrameInfoCommand() string {
	sessionManager := NewSessionManager(ui.ctx)
	return sessionManager.GetCurrentFrameInfo()
}

func (ui *UIManager) executeShowTimelineCommand() string {
	ui.ctx.FrameNavigation.ShowTimeline = !ui.ctx.FrameNavigation.ShowTimeline
	if ui.ctx.FrameNavigation.ShowTimeline {
		return "时间线视图已启用"
	} else {
		return "时间线视图已禁用"
	}
}

// ========== 搜索功能 ==========

// ExecuteSearch 在当前文件中执行搜索
func (ui *UIManager) ExecuteSearch(searchTerm string) {
	if ui.ctx.Project == nil || ui.ctx.Project.CurrentFile == "" {
		ui.ctx.CommandHistory = append(ui.ctx.CommandHistory, "没有打开的文件可供搜索")
		ui.ctx.CommandDirty = true
		return
	}
	
	fileManager := NewFileManager(ui.ctx)
	content, err := fileManager.GetCurrentFileContent()
	if err != nil {
		ui.ctx.CommandHistory = append(ui.ctx.CommandHistory, fmt.Sprintf("读取文件失败: %v", err))
		ui.ctx.CommandDirty = true
		return
	}
	
	// 清空之前的搜索结果
	ui.ctx.SearchResults = nil
	ui.ctx.CurrentMatch = 0
	
	// 搜索匹配项
	searchTerm = strings.ToLower(searchTerm)
	for lineNum, line := range content {
		lowercaseLine := strings.ToLower(line)
		startPos := 0
		
		for {
			index := strings.Index(lowercaseLine[startPos:], searchTerm)
			if index == -1 {
				break
			}
			
			actualIndex := startPos + index
			result := SearchResult{
				LineNumber:  lineNum + 1,
				StartColumn: actualIndex,
				EndColumn:   actualIndex + len(searchTerm),
				Text:        line[actualIndex:actualIndex+len(searchTerm)],
			}
			
			ui.ctx.SearchResults = append(ui.ctx.SearchResults, result)
			startPos = actualIndex + 1
		}
	}
	
	// 更新搜索状态
	ui.ctx.SearchTerm = searchTerm
	ui.ctx.SearchDirty = true
	
	// 显示搜索结果
	if len(ui.ctx.SearchResults) == 0 {
		ui.ctx.CommandHistory = append(ui.ctx.CommandHistory, fmt.Sprintf("未找到 '%s'", searchTerm))
	} else {
		ui.ctx.CommandHistory = append(ui.ctx.CommandHistory, fmt.Sprintf("找到 %d 个 '%s' 的匹配项", len(ui.ctx.SearchResults), searchTerm))
		ui.ctx.CurrentMatch = 0
	}
	
	ui.ctx.CommandDirty = true
}

// NextSearchResult 跳转到下一个搜索结果
func (ui *UIManager) NextSearchResult() {
	if len(ui.ctx.SearchResults) == 0 {
		return
	}
	
	ui.ctx.CurrentMatch = (ui.ctx.CurrentMatch + 1) % len(ui.ctx.SearchResults)
	ui.showCurrentSearchResult()
}

// PrevSearchResult 跳转到上一个搜索结果
func (ui *UIManager) PrevSearchResult() {
	if len(ui.ctx.SearchResults) == 0 {
		return
	}
	
	ui.ctx.CurrentMatch = (ui.ctx.CurrentMatch - 1 + len(ui.ctx.SearchResults)) % len(ui.ctx.SearchResults)
	ui.showCurrentSearchResult()
}

// showCurrentSearchResult 显示当前搜索结果
func (ui *UIManager) showCurrentSearchResult() {
	if len(ui.ctx.SearchResults) == 0 {
		return
	}
	
	result := ui.ctx.SearchResults[ui.ctx.CurrentMatch]
	ui.ctx.CommandHistory = append(ui.ctx.CommandHistory, 
		fmt.Sprintf("匹配项 %d/%d: 第%d行, 列%d-%d", 
			ui.ctx.CurrentMatch+1, len(ui.ctx.SearchResults),
			result.LineNumber, result.StartColumn, result.EndColumn))
	ui.ctx.CommandDirty = true
}

// ========== 命令执行扩展 ==========

func (ui *UIManager) executeSearchCommand(args []string) string {
	if len(args) == 0 {
		return "用法: search <搜索词>"
	}
	
	searchTerm := strings.Join(args, " ")
	ui.ExecuteSearch(searchTerm)
	return ""  // ExecuteSearch 会自己添加输出消息
} 