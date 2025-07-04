package main

import (
	"fmt"
	"strings"
	"path/filepath"
	
	"github.com/jroimartin/gocui"
)

// ========== 视图更新管理器 ==========

type ViewUpdater struct {
	ctx *DebuggerContext
	gui *gocui.Gui
}

// NewViewUpdater 创建视图更新管理器
func NewViewUpdater(ctx *DebuggerContext, gui *gocui.Gui) *ViewUpdater {
	return &ViewUpdater{ctx: ctx, gui: gui}
}

// ========== 文件浏览器视图更新 ==========

// UpdateFileBrowserView 更新文件浏览器视图
func (vu *ViewUpdater) UpdateFileBrowserView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("filebrowser")
	if err != nil {
		return
	}
	
	v.Clear()
	
	// 清空映射表
	fileBrowserLineMap = []*FileNode{}
	fileBrowserDisplayLines = []string{}
	
	// 不再显示内容中的标题，因为窗口标题已经动态设置
	
	// 显示项目信息
	if ctx.Project != nil {
		fmt.Fprintf(v, "📁 %s\n", filepath.Base(ctx.Project.RootPath))
		fmt.Fprintln(v, "")
		
		// 显示文件树
		if ctx.Project.FileTree != nil {
			vu.displayFileTreeWithMapping(v, ctx.Project.FileTree, 0, ctx)
		}
	} else {
		fmt.Fprintln(v, "No project opened")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "Commands:")
		fmt.Fprintln(v, "  open <path>  - Open project")
		fmt.Fprintln(v, "  help         - Show help")
	}
}

// displayFileTreeWithMapping 显示文件树并建立映射
func (vu *ViewUpdater) displayFileTreeWithMapping(v *gocui.View, node *FileNode, depth int, ctx *DebuggerContext) {
	vu.displayFileTreeNode(v, node, depth, ctx)
}

// displayFileTreeNode 递归显示文件树节点并建立映射
func (vu *ViewUpdater) displayFileTreeNode(v *gocui.View, node *FileNode, depth int, ctx *DebuggerContext) {
	if node == nil {
		return
	}
	
	indent := strings.Repeat("  ", depth)
	icon := "📄"
	highlight := ""
	
	if node.IsDir {
		if node.Expanded {
			icon = "📂"
		} else {
			icon = "📁"
		}
	} else {
		// 根据文件扩展名显示不同图标
		ext := strings.ToLower(filepath.Ext(node.Name))
		switch ext {
		case ".c":
			icon = "🔧"
		case ".cpp":
			icon = "⚙️"
		case ".h", ".hpp":
			icon = "📋"
		case ".go":
			icon = "🐹"
		case ".py":
			icon = "🐍"
		case ".js":
			icon = "📜"
		case ".md":
			icon = "📝"
		case ".txt":
			icon = "📃"
		default:
			icon = "📄"
		}
		
		// 检查是否是当前打开的文件
		if ctx.Project != nil && ctx.Project.CurrentFile == node.Path {
			highlight = "\x1b[32m" // 绿色高亮
		}
	}
	
	// 构建显示行
	displayLine := fmt.Sprintf("%s%s %s", indent, icon, node.Name)
	
	// 添加到映射表
	fileBrowserLineMap = append(fileBrowserLineMap, node)
	fileBrowserDisplayLines = append(fileBrowserDisplayLines, displayLine)
	
	// 显示行（考虑高亮）
	if highlight != "" {
		fmt.Fprintf(v, "%s%s\x1b[0m\n", highlight, displayLine)
	} else {
		fmt.Fprintf(v, "%s\n", displayLine)
	}
	
	// 如果是展开的目录，显示子节点
	if node.IsDir && node.Expanded {
		for _, child := range node.Children {
			vu.displayFileTreeNode(v, child, depth+1, ctx)
		}
	}
}

// ========== 代码视图更新 ==========

// UpdateCodeView 更新代码视图
func (vu *ViewUpdater) UpdateCodeView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("code")
	if err != nil {
		return
	}
	v.Clear()
	
	// 显示搜索状态（如果有的话）
	if ctx.SearchMode {
		searchStatus := ""
		if len(ctx.SearchResults) > 0 {
			searchStatus = fmt.Sprintf("Search: \"%s\" (%d/%d)", 
				ctx.SearchTerm, ctx.CurrentMatch+1, len(ctx.SearchResults))
		} else if ctx.SearchTerm != "" {
			searchStatus = fmt.Sprintf("Search: \"%s\" (no results)", ctx.SearchTerm)
		} else {
			searchStatus = fmt.Sprintf("Search: \"%s\"", ctx.SearchInput)
		}
		fmt.Fprintf(v, "%s\n", searchStatus)
	}
	
	// 如果有打开的文件，显示文件内容
	if ctx.Project != nil && ctx.Project.CurrentFile != "" {
		lines, exists := ctx.Project.OpenFiles[ctx.Project.CurrentFile]
		if !exists {
			// 尝试读取文件
			fileManager := NewFileManager(ctx)
			content, err := fileManager.GetCurrentFileContent()
			if err != nil {
				fmt.Fprintf(v, "Cannot read file: %v\n", err)
				return
			}
			lines = content
			ctx.Project.OpenFiles[ctx.Project.CurrentFile] = lines
		}
		
		fmt.Fprintf(v, "📄 %s\n", filepath.Base(ctx.Project.CurrentFile))
		
		// 显示代码行
		maxLines := len(lines)
		startLine := codeScroll
		if startLine >= maxLines {
			startLine = maxLines - 1
		}
		if startLine < 0 {
			startLine = 0
		}
		
		// 计算窗口可用的显示行数
		_, viewHeight := v.Size()
		headerLines := 2 // 标题行："代码视图" + 文件名行
		availableLines := viewHeight - headerLines
		if availableLines < 1 {
			availableLines = 1 // 至少显示1行
		}
		
		// 动态适应窗口高度显示代码
		endLine := startLine + availableLines
		if endLine > maxLines {
			endLine = maxLines
		}
		
		for i := startLine; i < endLine; i++ {
			lineNum := i + 1
			line := lines[i]
			
			// 检查是否有断点
			hasBreakpoint := false
			for _, bp := range ctx.Project.Breakpoints {
				if bp.File == ctx.Project.CurrentFile && bp.Line == lineNum && bp.Enabled {
					hasBreakpoint = true
					break
				}
			}
			
			// 应用搜索高亮
			highlightedLine := vu.highlightSearchMatches(line, lineNum, ctx)
			
			// 显示行号和断点标记
			if hasBreakpoint {
				fmt.Fprintf(v, "%3d● %s\n", lineNum, highlightedLine)
			} else {
				fmt.Fprintf(v, "%3d: %s\n", lineNum, highlightedLine)
			}
		}
		
	} else {
		// 默认显示汇编代码
		fmt.Fprintln(v, "Assembly Code (Example)")
		fmt.Fprintln(v, "")
		
		insts := []string{
			"addi sp, sp, -32",
			"sd   ra, 24(sp)",
			"sd   s0, 16(sp)",
			"addi s0, sp, 32",
			"li   a0, 0x1000",
			"call taco_sys_mmz_alloc",
			"mv   s1, a0",
			"beqz s1, .error",
			"li   a1, 64",
			"mv   a0, s1",
			"call memset",
			"ld   ra, 24(sp)",
			"ld   s0, 16(sp)",
			"addi sp, sp, 32",
			"ret",
		}
		
		// 计算窗口可用的显示行数（汇编代码）
		_, viewHeight := v.Size()
		headerLines := 3 // "代码视图" + "汇编代码 (示例)" + 空行
		availableLines := viewHeight - headerLines
		if availableLines < 1 {
			availableLines = 1
		}
		
		// 动态适应窗口高度显示汇编代码
		startLine := codeScroll
		if startLine >= len(insts) {
			startLine = len(insts) - 1
		}
		if startLine < 0 {
			startLine = 0
		}
		
		endLine := startLine + availableLines
		if endLine > len(insts) {
			endLine = len(insts)
		}
		
		for i := startLine; i < endLine; i++ {
			if i == codeScroll {
				fmt.Fprintf(v, "%3d=> 0x%016x: %s\n", i+1, ctx.CurrentAddr, insts[i])
			} else {
				fmt.Fprintf(v, "%3d:  0x%016x: %s\n", i+1, ctx.CurrentAddr+uint64(i*4), insts[i])
			}
		}
	}
}

// highlightSearchMatches 在代码行中高亮搜索匹配
func (vu *ViewUpdater) highlightSearchMatches(line string, lineNum int, ctx *DebuggerContext) string {
	if !ctx.SearchMode || ctx.SearchTerm == "" {
		return line
	}
	
	// 查找该行的搜索结果
	highlightedLine := line
	for _, result := range ctx.SearchResults {
		if result.LineNumber == lineNum {
			// 简单的高亮处理：在匹配文本周围添加颜色代码
			before := highlightedLine[:result.StartColumn]
			match := highlightedLine[result.StartColumn:result.EndColumn]
			after := highlightedLine[result.EndColumn:]
			
			// 使用黄色背景高亮匹配文本
			highlightedLine = before + "\x1b[43;30m" + match + "\x1b[0m" + after
			break // 每行只高亮第一个匹配
		}
	}
	
	return highlightedLine
}

// ========== 寄存器视图更新 ==========

// UpdateRegistersView 更新寄存器视图
func (vu *ViewUpdater) UpdateRegistersView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("registers")
	if err != nil {
		return
	}
	v.Clear()
	
	// 窗口标题由布局函数动态设置，这里不再显示
	
	// 显示寄存器内容
	if ctx.CurrentFrame != nil && ctx.CurrentFrame.Registers != nil {
		regs := ctx.CurrentFrame.Registers
		
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
	} else {
		// 显示默认/模拟寄存器状态
		lines := []string{
			fmt.Sprintf("PC: 0x%016x", ctx.CurrentAddr),
			fmt.Sprintf("RA: 0x%016x", ctx.CurrentAddr+0x100),
			fmt.Sprintf("SP: 0x%016x", ctx.CurrentAddr+0x200),
			fmt.Sprintf("GP: 0x%016x", ctx.CurrentAddr+0x300),
			fmt.Sprintf("TP: 0x%016x", ctx.CurrentAddr+0x400),
			"",
			fmt.Sprintf("T0: 0x%016x", ctx.CurrentAddr+0x500),
			fmt.Sprintf("T1: 0x%016x", ctx.CurrentAddr+0x600),
			fmt.Sprintf("T2: 0x%016x", ctx.CurrentAddr+0x700),
			"",
			fmt.Sprintf("S0: 0x%016x", ctx.CurrentAddr+0x800),
			fmt.Sprintf("S1: 0x%016x", ctx.CurrentAddr+0x900),
			"",
			fmt.Sprintf("A0: 0x%016x", ctx.CurrentAddr+0xA00),
			fmt.Sprintf("A1: 0x%016x", ctx.CurrentAddr+0xB00),
			fmt.Sprintf("A2: 0x%016x", ctx.CurrentAddr+0xC00),
			fmt.Sprintf("A3: 0x%016x", ctx.CurrentAddr+0xD00),
			fmt.Sprintf("A4: 0x%016x", ctx.CurrentAddr+0xE00),
			fmt.Sprintf("A5: 0x%016x", ctx.CurrentAddr+0xF00),
			fmt.Sprintf("A6: 0x%016x", ctx.CurrentAddr+0x1000),
			fmt.Sprintf("A7: 0x%016x", ctx.CurrentAddr+0x1100),
		}
		
		for i := regScroll; i < len(lines); i++ {
			fmt.Fprintln(v, lines[i])
		}
	}
}

// ========== 变量视图更新 ==========

// UpdateVariablesView 更新变量视图
func (vu *ViewUpdater) UpdateVariablesView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("variables")
	if err != nil {
		return
	}
	v.Clear()
	
	// 窗口标题由布局函数动态设置，这里不再显示
	
	// 显示变量内容
	if ctx.CurrentFrame != nil {
		// 显示局部变量
		fmt.Fprintln(v, "Local variables:")
		if len(ctx.CurrentFrame.LocalVariables) > 0 {
			for name, value := range ctx.CurrentFrame.LocalVariables {
				fmt.Fprintf(v, "  %s = %v\n", name, value)
			}
		} else {
			fmt.Fprintln(v, "  None")
		}
		
		fmt.Fprintln(v, "")
		
		// 显示全局变量
		fmt.Fprintln(v, "Global variables:")
		if len(ctx.CurrentFrame.GlobalVariables) > 0 {
			for name, value := range ctx.CurrentFrame.GlobalVariables {
				fmt.Fprintf(v, "  %s = %v\n", name, value)
			}
		} else {
			fmt.Fprintln(v, "  None")
		}
	} else {
		// 显示默认变量信息
		lines := []string{
			"Local variables:",
			"ctx      debugger_ctx_t* 0x7fff1234",
			"fd       int             3",
			"ret      int            -1",
			"buffer   char[256]       \"hello\"",
			"",
			"Global variables:",
			"g_ctx    debugger_ctx_t* 0x601020",
			"debug_level int          2",
			"config   config_t*       0x602000",
		}
		
		for i := varScroll; i < len(lines); i++ {
			fmt.Fprintln(v, lines[i])
		}
	}
}

// ========== 调用栈视图更新 ==========

// UpdateStackView 更新调用栈视图
func (vu *ViewUpdater) UpdateStackView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("stack")
	if err != nil {
		return
	}
	v.Clear()
	
	// 窗口标题由布局函数动态设置，这里不再显示
	
	// 显示调用栈内容
	if ctx.CurrentFrame != nil {
		// 显示调用链
		fmt.Fprintln(v, "Call chain:")
		if len(ctx.CurrentFrame.CallChain) > 0 {
			for i, call := range ctx.CurrentFrame.CallChain {
				fmt.Fprintf(v, "  %d. %s()\n", i+1, call.FunctionName)
				fmt.Fprintf(v, "     Return: 0x%016x\n", call.ReturnAddress)
			}
		} else {
			fmt.Fprintln(v, "  None")
		}
		
		fmt.Fprintln(v, "")
		
		// 显示栈帧
		fmt.Fprintln(v, "Stack frames:")
		if len(ctx.CurrentFrame.StackFrames) > 0 {
			for i, frame := range ctx.CurrentFrame.StackFrames {
				fmt.Fprintf(v, "  %d. %s @ 0x%016x\n", i+1, frame.FunctionName, frame.Address)
				if frame.FileName != "" {
					fmt.Fprintf(v, "     File: %s:%d\n", frame.FileName, frame.LineNumber)
				}
			}
		} else {
			fmt.Fprintln(v, "  None")
		}
	} else {
		// 显示默认调用栈信息
		lines := []string{
			"#0 taco_sys_init kernel_debugger_tui.c:156",
			"#1 taco_sys_mmz_alloc taco_sys_mmz.c:89",
			"#2 taco_sys_init taco_sys_init.c:45",
			"#3 main main.c:23",
			"#4 __libc_start_main libc.so.6",
			"#5 _start init.c:1",
		}
		
		for i := stackScroll; i < len(lines); i++ {
			fmt.Fprintln(v, lines[i])
		}
	}
}

// ========== 状态视图更新 ==========

// UpdateStatusView 更新状态视图
func (vu *ViewUpdater) UpdateStatusView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("status")
	if err != nil {
		return
	}
	
	v.Clear()
	
	var statusParts []string
	
	// 调试模式状态
	switch ctx.DebugMode {
	case "live":
		statusParts = append(statusParts, "🟢 Live")
	case "recording":
		statusParts = append(statusParts, "🔴 Recording")
	case "playback":
		statusParts = append(statusParts, "▶️  Playback")
	default:
		statusParts = append(statusParts, "⚪ Unknown")
	}
	
	// 显示调试器状态
	stateStr := "STOP"
	if ctx.BpfLoaded {
		stateStr = "BPF_LOADED"
	}
	if ctx.Running {
		stateStr = "RUNNING"
	}
	statusParts = append(statusParts, fmt.Sprintf("State: %s", stateStr))
	
	// 显示当前函数和地址
	statusParts = append(statusParts, fmt.Sprintf("Func: %s", ctx.CurrentFunc))
	statusParts = append(statusParts, fmt.Sprintf("Addr: 0x%X", ctx.CurrentAddr))
	
	// BPF程序状态
	bpfManager := NewBPFManager(ctx)
	if bpfManager.IsRunning() {
		statusParts = append(statusParts, "BPF: Running")
	} else {
		statusParts = append(statusParts, "BPF: Stopped")
	}
	
	// 断点信息
	if ctx.Project != nil {
		statusParts = append(statusParts, fmt.Sprintf("Breakpoints: %d", len(ctx.Project.Breakpoints)))
	}
	
	// 显示全屏状态和操作提示
	if ctx.IsFullscreen {
		statusParts = append(statusParts, fmt.Sprintf("Fullscreen: %s", ctx.FullscreenView))
		statusParts = append(statusParts, "F11/ESC-Exit")
	} else {
		// 显示拖拽状态和提示
		if ctx.Layout != nil {
			if ctx.Layout.IsDragging {
				statusParts = append(statusParts, fmt.Sprintf("Resizing: %s", vu.getBoundaryName(ctx.Layout.DragBoundary)))
			} else {
				statusParts = append(statusParts, "Tip: Drag borders to resize, F11 for fullscreen")
			}
			
			// 显示当前布局参数
			statusParts = append(statusParts, fmt.Sprintf("Layout: L%d R%d C%d", 
				ctx.Layout.LeftPanelWidth, 
				ctx.Layout.RightPanelWidth, 
				ctx.Layout.CommandHeight))
		}
	}
	
	// 帧信息
	sessionManager := NewSessionManager(ctx)
	frameInfo := sessionManager.GetCurrentFrameInfo()
	if frameInfo != "No debug session available" {
		statusParts = append(statusParts, frameInfo)
	}
	
	// 快捷键提示
	statusParts = append(statusParts, "F9:PrevFrame F10:NextFrame F1:Help")
	
	fmt.Fprint(v, strings.Join(statusParts, " | "))
}

// getBoundaryName 获取边界名称的友好显示
func (vu *ViewUpdater) getBoundaryName(boundary string) string {
	switch boundary {
	case "left":
		return "Left Border"
	case "right":
		return "Right Border"
	case "bottom":
		return "Bottom Border"
	case "right1":
		return "Reg/Var Split"
	case "right2":
		return "Var/Stack Split"
	default:
		return "Unknown Border"
	}
}

// ========== 命令视图更新 ==========

// UpdateCommandView 更新命令视图
func (vu *ViewUpdater) UpdateCommandView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("command")
	if err != nil {
		return
	}
	
	// 检查是否是当前聚焦窗口
	currentView := g.CurrentView()
	isCurrentView := currentView != nil && currentView.Name() == "command"
	
	if isCurrentView {
		// 检测粘贴内容（只在非Dirty状态下检测，避免循环）
		if !ctx.CommandDirty {
			// 获取视图缓冲区内容
			viewBuffer := v.ViewBuffer()
			
			// 简化的粘贴检测：直接从缓冲区末尾提取当前行
			lines := strings.Split(strings.TrimSuffix(viewBuffer, "\n"), "\n")
			if len(lines) > 0 {
				lastLine := lines[len(lines)-1]
				// 检查最后一行是否以 "> " 开头
				if strings.HasPrefix(lastLine, "> ") {
					actualInput := lastLine[2:] // 去掉 "> " 前缀
					
					// 如果实际输入与CurrentInput不同，说明有粘贴操作
					if actualInput != ctx.CurrentInput {
						// 调试信息：记录重要的输入变化
						if len(actualInput) > 40 && len(ctx.CommandHistory) < 10 {
							debugInfo := fmt.Sprintf("[DEBUG] Paste detected: length=%d, content=%s", len(actualInput), actualInput)
							ctx.CommandHistory = append(ctx.CommandHistory, debugInfo)
						}
						ctx.CurrentInput = actualInput
						ctx.CommandDirty = true // 标记需要重新同步光标位置
					}
				}
			}
		}
		
		// 只有在CommandDirty为true时才重绘，避免频繁Clear()
		if ctx.CommandDirty {
			// 清空视图并重新绘制
			v.Clear()
			
			// 显示历史记录
			for _, historyLine := range ctx.CommandHistory {
				fmt.Fprintln(v, historyLine)
			}
			
			// 显示当前输入行
			fmt.Fprintf(v, "> %s", ctx.CurrentInput)
			
			// 设置光标位置到当前输入的末尾
			cursorX := 2 + len(ctx.CurrentInput)  // "> " + 输入内容
			cursorY := len(ctx.CommandHistory)    // 历史记录行数
			v.SetCursor(cursorX, cursorY)
			
			// 标记已更新
			ctx.CommandDirty = false
		}
		
	} else {
		// 如果不是聚焦状态，显示简化的帮助信息
		v.Clear()
		
		fmt.Fprintln(v, "Command Terminal - Press F6 to focus")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "Basic commands:")
		fmt.Fprintln(v, "  help         - Show help")
		fmt.Fprintln(v, "  open <path>  - Open project")
		fmt.Fprintln(v, "  clear        - Clear screen")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "Shortcuts: Tab-Switch windows")
		
		// 显示项目状态
		if ctx.Project != nil {
			fmt.Fprintln(v, "")
			fmt.Fprintf(v, "Project: %s", filepath.Base(ctx.Project.RootPath))
		}
		
		// 显示最近的几条命令历史（如果有的话）
		if len(ctx.CommandHistory) > 0 {
			fmt.Fprintln(v, "")
			fmt.Fprintln(v, "Recent commands:")
			// 显示最后3条历史记录
			start := len(ctx.CommandHistory) - 3
			if start < 0 {
				start = 0
			}
			for i := start; i < len(ctx.CommandHistory) && i < start+3; i++ {
				line := ctx.CommandHistory[i]
				if len(line) > 30 {
					line = line[:27] + "..."
				}
				fmt.Fprintf(v, "  %s\n", line)
			}
		}
	}
}

// ========== 断点视图更新 ==========

// UpdateBreakpointsView 更新断点视图
func (vu *ViewUpdater) UpdateBreakpointsView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("breakpoints")
	if err != nil {
		return
	}
	v.Clear()
	
	fmt.Fprintln(v, "Breakpoints")
	fmt.Fprintln(v, strings.Repeat("-", 30))
	
	if ctx.Project != nil && len(ctx.Project.Breakpoints) > 0 {
		for i, bp := range ctx.Project.Breakpoints {
			status := "❌"
			if bp.Enabled {
				status = "✅"
			}
			fmt.Fprintf(v, "%d. %s %s:%d\n", i+1, status, filepath.Base(bp.File), bp.Line)
		}
	} else {
		fmt.Fprintln(v, "No breakpoints set")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "Double-click on code lines to set breakpoints")
	}
}

// ========== 综合更新函数 ==========

// UpdateAllViews 更新所有视图
func (vu *ViewUpdater) UpdateAllViews(g *gocui.Gui, ctx *DebuggerContext) {
	vu.UpdateStatusView(g, ctx)
	vu.UpdateFileBrowserView(g, ctx)
	vu.UpdateRegistersView(g, ctx)
	vu.UpdateVariablesView(g, ctx)
	vu.UpdateStackView(g, ctx)
	vu.UpdateBreakpointsView(g, ctx)
	vu.UpdateCodeView(g, ctx)
	vu.UpdateCommandView(g, ctx)
} 