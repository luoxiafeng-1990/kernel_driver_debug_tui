package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"path/filepath"
	"encoding/json"
	"io/ioutil"
	"math/rand"

	"github.com/jroimartin/gocui"
)

// ========== 主程序入口 ==========

func main() {
	// 初始化随机数种子
	rand.Seed(time.Now().UnixNano())
	
	// 处理命令行参数
	if len(os.Args) > 1 {
		projectPath := os.Args[1]
		// 验证路径存在
		if info, err := os.Stat(projectPath); err == nil && info.IsDir() {
			// 延迟到GUI初始化后再打开项目
			defer func() {
				// 创建调试器上下文后再初始化项目
			}()
		} else {
			fmt.Printf("Error: Invalid project path: %s\n", projectPath)
			os.Exit(1)
		}
	}
	
	g, err := gocui.NewGui(gocui.OutputNormal)
	if err != nil {
		log.Fatalln(err)
	}
	defer g.Close()

	// 启用鼠标支持
	g.Mouse = true

	// 创建调试器上下文
	ctx := &DebuggerContext{
		State:         DEBUG_STOPPED,
		CurrentFocus:  0,
		BpfLoaded:     false,
		MouseEnabled:  true,
		CommandHistory: []string{"Welcome to RISC-V Kernel Debugger TUI v2.0", "Type 'help' to see available commands"},
		CommandDirty: true,
		DebugMode:    "live",
		GUI:          g,
	}

	// 如果有命令行参数，自动打开项目
	if len(os.Args) > 1 {
		projectPath := os.Args[1]
		fileManager := NewFileManager(ctx)
		if err := fileManager.InitProject(projectPath); err != nil {
					ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Failed to auto-open project: %v", err))
	} else {
		ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Auto-opened project: %s", projectPath))
		}
		ctx.CommandDirty = true
	}

	// 设置全局上下文引用
	globalCtx = ctx

	// 设置布局函数
	g.SetManagerFunc(func(g *gocui.Gui) error {
		return layout(g, ctx)
	})

	// 绑定键盘事件
	bindKeys(g, ctx)

	// 初始化会话管理器
	sessionManager := NewSessionManager(ctx)
	sessionManager.InitDebugSession()

	// 设置信号处理，优雅退出
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		g.Update(func(g *gocui.Gui) error {
			return gocui.ErrQuit
		})
	}()

	// 主循环
	if err := g.MainLoop(); err != nil && err != gocui.ErrQuit {
		log.Fatalln(err)
	}

	// 清理资源
	cleanup(ctx)
}

// ========== GUI布局管理 ==========

func layout(g *gocui.Gui, ctx *DebuggerContext) error {
	maxX, maxY := g.Size()

	// 初始化动态布局（如果还没有初始化）
	if ctx.Layout == nil {
		ctx.Layout = initDynamicLayout(maxX, maxY)
	}

	// 如果处于全屏状态，只显示全屏窗口
	if ctx.IsFullscreen && ctx.FullscreenView != "" {
		return layoutFullscreen(g, ctx.FullscreenView, maxX, maxY)
	}

	// 计算窗口位置 - 添加边界检查防止崩溃
	leftWidth := ctx.Layout.LeftPanelWidth
	rightWidth := ctx.Layout.RightPanelWidth
	cmdHeight := ctx.Layout.CommandHeight
	
	// 🔧 关键修复：确保所有计算值都是正数
	middleWidth := maxX - leftWidth - rightWidth
	if middleWidth < 10 {
		// 如果中间宽度太小，动态调整左右面板
		adjustment := (10 - middleWidth) / 2
		leftWidth -= adjustment
		rightWidth -= adjustment
		if leftWidth < 15 {
			leftWidth = 15
		}
		if rightWidth < 15 {
			rightWidth = 15
		}
		middleWidth = maxX - leftWidth - rightWidth
		if middleWidth < 10 {
			middleWidth = 10
		}
	}
	
	middleHeight := maxY - cmdHeight
	if middleHeight < 5 {
		// 如果上半部分高度太小，强制调整命令窗口高度
		cmdHeight = maxY - 5
		if cmdHeight < 3 {
			cmdHeight = 3
		}
		middleHeight = maxY - cmdHeight
		// 同步更新Layout中的值
		ctx.Layout.CommandHeight = cmdHeight
	}

	// 文件浏览器 (左侧)
	if v, err := g.SetView("filebrowser", 0, 0, leftWidth-1, middleHeight-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
		v.SelFgColor = gocui.ColorBlack
	}
	
	// 代码窗口 (中间)
	if v, err := g.SetView("code", leftWidth, 0, leftWidth+middleWidth-1, middleHeight-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = false
		v.Highlight = true
		v.SelBgColor = gocui.ColorYellow
		v.SelFgColor = gocui.ColorBlack
	}

	// 右侧面板分割 - 添加边界检查
	rightSplit1 := ctx.Layout.RightPanelSplit1
	rightSplit2 := ctx.Layout.RightPanelSplit2
	rightStart := leftWidth + middleWidth
	
	// 🔧 关键修复：确保右侧面板分割点有效
	if rightSplit1 >= middleHeight {
		rightSplit1 = middleHeight / 3
	}
	if rightSplit2 >= middleHeight {
		rightSplit2 = 2 * middleHeight / 3
	}
	if rightSplit1 < 2 {
		rightSplit1 = 2
	}
	if rightSplit2 <= rightSplit1 + 1 {
		rightSplit2 = rightSplit1 + 2
	}
	if rightSplit2 >= middleHeight - 1 {
		rightSplit2 = middleHeight - 2
	}

	// 寄存器窗口 (右上)
	if v, err := g.SetView("registers", rightStart, 0, maxX-1, rightSplit1-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = false
		v.Highlight = true
		v.SelBgColor = gocui.ColorCyan
		v.SelFgColor = gocui.ColorBlack
	}

	// 变量窗口 (右中)
	if v, err := g.SetView("variables", rightStart, rightSplit1, maxX-1, rightSplit2-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = false
		v.Highlight = true
		v.SelBgColor = gocui.ColorMagenta
		v.SelFgColor = gocui.ColorBlack
	}

	// 堆栈窗口 (右下)
	if v, err := g.SetView("stack", rightStart, rightSplit2, maxX-1, middleHeight-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Wrap = false
		v.Highlight = true
		v.SelBgColor = gocui.ColorRed
		v.SelFgColor = gocui.ColorBlack
	}

	// 命令窗口 (底部) - 关键修复：设置为可编辑
	if v, err := g.SetView("command", 0, middleHeight, maxX-1, maxY-2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Editable = true  // 🔧 修复：设置为可编辑
		v.Wrap = true
		v.Autoscroll = true
		v.Highlight = true
		v.SelBgColor = gocui.ColorBlue
		v.SelFgColor = gocui.ColorWhite
		
		// 🔧 修复：设置命令窗口为默认焦点
		g.SetCurrentView("command")
	}

	// 状态栏 (最底部)
	if v, err := g.SetView("status", 0, maxY-2, maxX-1, maxY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Frame = false
		v.BgColor = gocui.ColorBlue
		v.FgColor = gocui.ColorWhite
	}

	// 设置动态窗口标题（根据焦点状态）
	currentView := g.CurrentView()
	currentName := ""
	if currentView != nil {
		currentName = currentView.Name()
	}
	
	// 为每个窗口设置标题（根据是否为当前焦点）
	windowTitles := map[string]string{
		"filebrowser": "File Browser",
		"code":        "Code View", 
		"registers":   "Registers",
		"variables":   "Variables",
		"stack":       "Call Stack",
		"command":     "Command Terminal",
	}
	
	for viewName, baseTitle := range windowTitles {
		if v, err := g.View(viewName); err == nil {
			if currentName == viewName {
				// 使用更明显的标题高亮效果（背景色+前景色）
				v.Title = "\x1b[1;43;30m▶ " + baseTitle + " (Focused) \x1b[0m"
				// 设置边框为明亮的黄色
				v.FgColor = gocui.ColorYellow | gocui.AttrBold
				// 保持选择区域的高亮
				v.SelBgColor = gocui.ColorYellow
				v.SelFgColor = gocui.ColorBlack
			} else {
				v.Title = baseTitle
				// 恢复默认边框颜色
				v.FgColor = gocui.ColorWhite
				// 保持原有的选择颜色配置
				switch viewName {
				case "filebrowser":
					v.SelBgColor = gocui.ColorGreen
					v.SelFgColor = gocui.ColorBlack
				case "code":
					v.SelBgColor = gocui.ColorYellow
					v.SelFgColor = gocui.ColorBlack
				case "registers":
					v.SelBgColor = gocui.ColorCyan
					v.SelFgColor = gocui.ColorBlack
				case "variables":
					v.SelBgColor = gocui.ColorMagenta
					v.SelFgColor = gocui.ColorBlack
				case "stack":
					v.SelBgColor = gocui.ColorRed
					v.SelFgColor = gocui.ColorBlack
				case "command":
					v.SelBgColor = gocui.ColorBlue
					v.SelFgColor = gocui.ColorWhite
				}
			}
		}
	}

	// 更新所有视图
	updateAllViews(g, ctx)

	// 渲染弹出窗口
	renderPopupWindows(g, ctx)

	return nil
}

// ========== 键盘事件绑定 ==========

func bindKeys(g *gocui.Gui, ctx *DebuggerContext) {
	// 基本控制键
	if err := g.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, quit); err != nil {
		log.Panicln(err)
	}
	
	// ESC键 - 关闭弹出窗口或退出全屏
	if err := g.SetKeybinding("", gocui.KeyEsc, gocui.ModNone, handleEscapeKey(ctx)); err != nil {
		log.Panicln(err)
	}
	
	if err := g.SetKeybinding("", gocui.KeyTab, gocui.ModNone, nextViewHandler); err != nil {
		log.Panicln(err)
	}

	// 🔧 新增：动态窗口大小调整键盘绑定
	// Ctrl+J/K - 调整命令窗口高度 (修复方向：J=向上增加高度, K=向下减少高度)
	if err := g.SetKeybinding("", gocui.KeyCtrlJ, gocui.ModNone, adjustCommandHeightUp(ctx)); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyCtrlK, gocui.ModNone, adjustCommandHeightDown(ctx)); err != nil {
		log.Panicln(err)
	}
	
	// Ctrl+H/L - 调整左右面板宽度
	if err := g.SetKeybinding("", gocui.KeyCtrlH, gocui.ModNone, adjustLeftPanelWidthDown(ctx)); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyCtrlL, gocui.ModNone, adjustLeftPanelWidthUp(ctx)); err != nil {
		log.Panicln(err)
	}

	// 功能键
	if err := g.SetKeybinding("", gocui.KeyF1, gocui.ModNone, showHelpHandler(ctx)); err != nil {
		log.Panicln(err)
	}

	if err := g.SetKeybinding("", gocui.KeyF9, gocui.ModNone, prevFrameHandler(ctx)); err != nil {
		log.Panicln(err)
	}

	if err := g.SetKeybinding("", gocui.KeyF10, gocui.ModNone, nextFrameHandler(ctx)); err != nil {
		log.Panicln(err)
	}
	
	// 搜索功能
	if err := g.SetKeybinding("", gocui.KeyCtrlF, gocui.ModNone, startSearchHandler(ctx)); err != nil {
		log.Panicln(err)
	}

	// 命令输入
	if err := g.SetKeybinding("command", gocui.KeyEnter, gocui.ModNone, handleCommand(ctx)); err != nil {
		log.Panicln(err)
	}

	// 文件浏览器
	if err := g.SetKeybinding("filebrowser", gocui.KeyEnter, gocui.ModNone, handleFileSelection(ctx)); err != nil {
		log.Panicln(err)
	}
	
	// 文件夹展开/收缩
	if err := g.SetKeybinding("filebrowser", gocui.KeySpace, gocui.ModNone, handleFileToggle(ctx)); err != nil {
		log.Panicln(err)
	}

	// 代码视图
	if err := g.SetKeybinding("code", gocui.KeyEnter, gocui.ModNone, handleBreakpointToggle(ctx)); err != nil {
		log.Panicln(err)
	}

	// 鼠标事件
	if err := g.SetKeybinding("", gocui.MouseLeft, gocui.ModNone, mouseDownHandler(ctx)); err != nil {
		log.Panicln(err)
	}

	if err := g.SetKeybinding("", gocui.MouseRelease, gocui.ModNone, mouseUpHandler(ctx)); err != nil {
		log.Panicln(err)
	}

	// 滚动事件 - 全局绑定
	if err := g.SetKeybinding("", gocui.MouseWheelUp, gocui.ModNone, mouseScrollUpHandler); err != nil {
		log.Panicln(err)
	}

	if err := g.SetKeybinding("", gocui.MouseWheelDown, gocui.ModNone, mouseScrollDownHandler); err != nil {
		log.Panicln(err)
	}

	// 为每个视图绑定特定的鼠标滚动事件
	views := []string{"filebrowser", "code", "registers", "variables", "stack", "command"}
	for _, viewName := range views {
		if err := g.SetKeybinding(viewName, gocui.MouseWheelUp, gocui.ModNone, mouseScrollUpHandler); err != nil {
			log.Panicln(err)
		}
		if err := g.SetKeybinding(viewName, gocui.MouseWheelDown, gocui.ModNone, mouseScrollDownHandler); err != nil {
			log.Panicln(err)
		}
	}

	// 字符输入（命令）
	for ch := 'a'; ch <= 'z'; ch++ {
		if err := g.SetKeybinding("command", gocui.Key(ch), gocui.ModNone, handleCharInput(ch, ctx)); err != nil {
			log.Panicln(err)
		}
	}
	for ch := 'A'; ch <= 'Z'; ch++ {
		if err := g.SetKeybinding("command", gocui.Key(ch), gocui.ModNone, handleCharInput(ch, ctx)); err != nil {
			log.Panicln(err)
		}
	}
	for ch := '0'; ch <= '9'; ch++ {
		if err := g.SetKeybinding("command", gocui.Key(ch), gocui.ModNone, handleCharInput(ch, ctx)); err != nil {
			log.Panicln(err)
		}
	}
	
	// 特殊字符
	specialChars := []rune{' ', '-', '_', '.', '/', '\\', ':', '=', '<', '>', '(', ')', '[', ']', '{', '}', '"', '\'', ',', ';'}
	for _, ch := range specialChars {
		if err := g.SetKeybinding("command", gocui.Key(ch), gocui.ModNone, handleCharInput(ch, ctx)); err != nil {
			log.Panicln(err)
		}
	}

	// 退格键
	if err := g.SetKeybinding("command", gocui.KeyBackspace, gocui.ModNone, handleBackspace(ctx)); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("command", gocui.KeyBackspace2, gocui.ModNone, handleBackspace(ctx)); err != nil {
		log.Panicln(err)
	}
}

// ========== 事件处理函数 ==========

func quit(g *gocui.Gui, v *gocui.View) error {
	return gocui.ErrQuit
}

func showHelpHandler(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		uiManager := NewUIManager(ctx, g)
		helpLines := uiManager.ShowHelp()
		helpContent := strings.Join(helpLines, "\n")
		
		popup := createPopupWindow(ctx, "help", "Help", 80, 30, strings.Split(helpContent, "\n"))
		showPopupWindow(ctx, popup)
		
		return nil
	}
}

func nextViewHandler(g *gocui.Gui, v *gocui.View) error {
	views := []string{"filebrowser", "code", "registers", "variables", "stack", "command"}
	current := ""
	if v != nil {
		current = v.Name()
	}
	
	nextIndex := 0
	for i, name := range views {
		if name == current {
			nextIndex = (i + 1) % len(views)
			break
		}
	}
	
	_, err := g.SetCurrentView(views[nextIndex])
	return err
}

func prevFrameHandler(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		sessionManager := NewSessionManager(ctx)
		err := sessionManager.PrevFrame()
		if err != nil {
					ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Previous frame failed: %v", err))
	} else {
		ctx.CommandHistory = append(ctx.CommandHistory, "Jumped to previous frame")
		}
		ctx.CommandDirty = true
		return nil
	}
}

func nextFrameHandler(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		sessionManager := NewSessionManager(ctx)
		err := sessionManager.NextFrame()
		if err != nil {
					ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Next frame failed: %v", err))
	} else {
		ctx.CommandHistory = append(ctx.CommandHistory, "Jumped to next frame")
		}
		ctx.CommandDirty = true
		return nil
	}
}

func handleCommand(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		command := strings.TrimSpace(ctx.CurrentInput)
		if command != "" {
			uiManager := NewUIManager(ctx, g)
			uiManager.ExecuteCommand(command)
			
			// 清空当前输入
			ctx.CurrentInput = ""
			ctx.CommandDirty = true
			
			// 切换焦点到命令窗口以便继续输入
			g.SetCurrentView("command")
		}
		return nil
	}
}

func handleFileSelection(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		_, cy := v.Cursor()
		line, err := v.Line(cy)
		if err != nil {
			return err
		}
		
		line = strings.TrimSpace(line)
		if line == "" {
			return nil
		}
		
		// 解析文件树行，提取文件路径
		filePath := extractFilePathFromTreeLine(line, ctx.Project)
		if filePath == "" {
			return nil
		}
		
		fileManager := NewFileManager(ctx)
		
		// 检查是否是目录
		if info, err := os.Stat(filePath); err == nil && info.IsDir() {
			// 切换目录展开状态
			err := fileManager.ToggleFileExpansion(filePath)
			if err != nil {
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Toggle directory failed: %v", err))
			}
		} else {
			// 尝试打开文件
			err := fileManager.OpenFile(filePath)
			if err != nil {
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Open file failed: %v", err))
			} else {
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Opened file: %s", filePath))
			}
		}
		
		ctx.CommandDirty = true
		return nil
	}
}

func handleFileToggle(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		_, cy := v.Cursor()
		line, err := v.Line(cy)
		if err != nil {
			return err
		}
		
		line = strings.TrimSpace(line)
		filePath := extractFilePathFromTreeLine(line, ctx.Project)
		if filePath == "" {
			return nil
		}
		
		// 切换文件夹展开状态
		fileManager := NewFileManager(ctx)
		err = fileManager.ToggleFileExpansion(filePath)
		if err != nil {
			ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("切换目录失败: %v", err))
			ctx.CommandDirty = true
		}
		
		return nil
	}
}

func handleEscapeKey(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		// 如果有弹出窗口，关闭最后一个
		if len(ctx.PopupWindows) > 0 {
			ctx.PopupWindows = ctx.PopupWindows[:len(ctx.PopupWindows)-1]
			return nil
		}
		
		// 如果在全屏状态，退出全屏
		if ctx.IsFullscreen {
			ctx.IsFullscreen = false
			ctx.FullscreenView = ""
			if ctx.SavedLayout != nil {
				ctx.Layout = ctx.SavedLayout
				ctx.SavedLayout = nil
			}
			return nil
		}
		
		// 如果在搜索模式，退出搜索
		if ctx.SearchMode {
			ctx.SearchMode = false
			ctx.SearchTerm = ""
			ctx.SearchInput = ""
			ctx.SearchResults = nil
			ctx.CurrentMatch = 0
			ctx.SearchDirty = true
		}
		
		return nil
	}
}

func startSearchHandler(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx.Project == nil || ctx.Project.CurrentFile == "" {
			ctx.CommandHistory = append(ctx.CommandHistory, "请先打开文件再进行搜索")
			ctx.CommandDirty = true
			return nil
		}
		
		// 启动搜索模式
		ctx.SearchMode = true
		ctx.SearchInput = ""
		ctx.CommandHistory = append(ctx.CommandHistory, "搜索模式已启动 (输入搜索词，ESC退出)")
		ctx.CommandDirty = true
		
		return nil
	}
}

func handleBreakpointToggle(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx.Project == nil || ctx.Project.CurrentFile == "" {
			return nil
		}
		
		_, cy := v.Cursor()
		// 获取实际的行号（需要考虑滚动偏移）
		actualLine := cy + 1  // 简化处理
		
		fileManager := NewFileManager(ctx)
		if fileManager.HasBreakpoint(ctx.Project.CurrentFile, actualLine) {
			err := fileManager.RemoveBreakpoint(ctx.Project.CurrentFile, actualLine)
			if err == nil {
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Removed breakpoint at line %d", actualLine))
			}
		} else {
			functionName := fmt.Sprintf("line_%d", actualLine)
			err := fileManager.AddBreakpoint(ctx.Project.CurrentFile, actualLine, functionName)
			if err == nil {
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("Added breakpoint at line %d", actualLine))
			}
		}
		
		ctx.CommandDirty = true
		return nil
	}
}

func handleCharInput(ch rune, ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		ctx.CurrentInput += string(ch)
		ctx.CommandDirty = true
		return nil
	}
}

func handleBackspace(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if len(ctx.CurrentInput) > 0 {
			ctx.CurrentInput = ctx.CurrentInput[:len(ctx.CurrentInput)-1]
			ctx.CommandDirty = true
		}
		return nil
	}
}

func mouseDownHandler(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		// 鼠标点击窗口聚焦
		if v != nil {
			// 设置当前视图（聚焦）
			g.SetCurrentView(v.Name())
			
			// 创建事件处理器并处理鼠标点击
			eventHandler := NewEventHandler(ctx, g)
			return eventHandler.MouseDownHandler()
		}
		return nil
	}
}

func mouseUpHandler(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		// 鼠标释放处理
		return nil
	}
}

func mouseScrollUpHandler(g *gocui.Gui, v *gocui.View) error {
	if v != nil {
		// 设置当前视图以确保焦点正确
		g.SetCurrentView(v.Name())
		
		// 使用智能滚动
		ctx := globalCtx
		if ctx != nil {
			eh := NewEventHandler(ctx, g)
			return eh.ScrollUpHandler()
		}
		
		// 备用方案
		ox, oy := v.Origin()
		if oy > 0 {
			v.SetOrigin(ox, oy-1)
		}
	}
	return nil
}

func mouseScrollDownHandler(g *gocui.Gui, v *gocui.View) error {
	if v != nil {
		// 设置当前视图以确保焦点正确
		g.SetCurrentView(v.Name())
		
		// 使用智能滚动
		ctx := globalCtx
		if ctx != nil {
			eh := NewEventHandler(ctx, g)
			return eh.ScrollDownHandler()
		}
		
		// 备用方案
		ox, oy := v.Origin()
		v.SetOrigin(ox, oy+1)
	}
	return nil
}

// ========== 视图更新 ==========

func updateAllViews(g *gocui.Gui, ctx *DebuggerContext) {
	// 使用新的ViewUpdater而不是UIManager
	viewUpdater := NewViewUpdater(ctx, g)
	
	// 更新文件浏览器
	viewUpdater.UpdateFileBrowserView(g, ctx)
	
	// 更新代码视图
	viewUpdater.UpdateCodeView(g, ctx)
	
	// 更新寄存器视图
	viewUpdater.UpdateRegistersView(g, ctx)
	
	// 更新变量视图
	viewUpdater.UpdateVariablesView(g, ctx)
	
	// 更新堆栈视图
	viewUpdater.UpdateStackView(g, ctx)
	
	// 更新命令视图
	viewUpdater.UpdateCommandView(g, ctx)
	
	// 更新状态栏
	viewUpdater.UpdateStatusView(g, ctx)
}

// ========== 辅助函数 ==========

func initDynamicLayout(maxX, maxY int) *DynamicLayout {
	// 边界检查，确保不会出现负数或零值
	if maxX <= 0 {
		maxX = 80 // 默认宽度
	}
	if maxY <= 0 {
		maxY = 24 // 默认高度
	}
	
	leftWidth := maxX / 4
	if leftWidth < 15 {
		leftWidth = 15
	}
	
	rightWidth := maxX / 3
	if rightWidth < 20 {
		rightWidth = 20
	}
	
	cmdHeight := 8
	if cmdHeight > maxY/2 {
		cmdHeight = maxY / 2
	}
	if cmdHeight < 5 {
		cmdHeight = 5
	}
	
	availableHeight := maxY - cmdHeight
	if availableHeight < 10 {
		availableHeight = 10
	}
	
	// 计算右侧面板分割点，确保有效
	split1 := availableHeight / 3
	split2 := 2 * availableHeight / 3
	
	// 边界检查分割点
	if split1 < 2 {
		split1 = 2
	}
	if split2 <= split1 + 1 {
		split2 = split1 + 2
	}
	if split2 >= availableHeight - 1 {
		split2 = availableHeight - 2
		if split2 <= split1 + 1 {
			split1 = split2 - 2
		}
	}
	
	return &DynamicLayout{
		LeftPanelWidth:   leftWidth,
		RightPanelWidth:  rightWidth,
		CommandHeight:    cmdHeight,
		RightPanelSplit1: split1,
		RightPanelSplit2: split2,
	}
}

func layoutFullscreen(g *gocui.Gui, viewName string, maxX, maxY int) error {
	if v, err := g.SetView(viewName+"_fullscreen", 0, 0, maxX-1, maxY-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "全屏 - " + viewName + " (ESC退出)"
		v.Wrap = false
	}
	return nil
}

func createPopupWindow(ctx *DebuggerContext, id, title string, width, height int, content []string) *PopupWindow {
	maxX, maxY := ctx.GUI.Size()
	x := (maxX - width) / 2
	y := (maxY - height) / 2
	
	popup := &PopupWindow{
		ID:      id,
		Title:   title,
		X:       x,
		Y:       y,
		Width:   width,
		Height:  height,
		Content: content,
		Visible: true,
	}
	
	return popup
}

func showPopupWindow(ctx *DebuggerContext, popup *PopupWindow) {
	ctx.PopupWindows = append(ctx.PopupWindows, popup)
}

func renderPopupWindows(g *gocui.Gui, ctx *DebuggerContext) error {
	for _, popup := range ctx.PopupWindows {
		if !popup.Visible {
			continue
		}
		
		// 创建弹出窗口视图
		viewName := "popup_" + popup.ID
		if v, err := g.SetView(viewName, popup.X, popup.Y, popup.X+popup.Width, popup.Y+popup.Height); err != nil {
			if err != gocui.ErrUnknownView {
				return err
			}
			v.Title = popup.Title
			v.Wrap = true
		} else {
			v.Clear()
			
			// 显示内容
			startLine := popup.ScrollY
			endLine := startLine + popup.Height - 2
			
			for i := startLine; i < endLine && i < len(popup.Content); i++ {
				fmt.Fprintln(v, popup.Content[i])
			}
		}
	}
	
	return nil
}

// ========== 清理函数 ==========

func cleanup(ctx *DebuggerContext) {
	// 停止BPF程序
	if ctx.BPFCtx != nil {
		bpfManager := NewBPFManager(ctx)
		bpfManager.StopBPFProgram()
	}
	
	// 保存断点配置
	if ctx.Project != nil {
		saveBreakpoints(ctx)
	}
}

func saveBreakpoints(ctx *DebuggerContext) error {
	if ctx.Project == nil {
		return nil
	}
	
	configPath := filepath.Join(ctx.Project.RootPath, ".debugger_config.json")
	
	config := map[string]interface{}{
		"breakpoints": ctx.Project.Breakpoints,
		"last_file":   ctx.Project.CurrentFile,
	}
	
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	
	return ioutil.WriteFile(configPath, data, 0644)
}

// ========== 已有的函数保持不变 ==========
// 这里保留原始main.go中的一些必要函数，但简化它们

// ... 其他必要的辅助函数 ...

// 从文件树行中提取文件路径
func extractFilePathFromTreeLine(line string, project *ProjectInfo) string {
	if project == nil || project.FileTree == nil {
		return ""
	}
	
	// 移除图标和缩进，提取文件名
	line = strings.TrimSpace(line)
	
	// 移除表情符号图标
	icons := []string{"📁", "📂", "📄", "🐹", "🐍", "📜", "📝", "📃"}
	for _, icon := range icons {
		line = strings.ReplaceAll(line, icon, "")
	}
	
	fileName := strings.TrimSpace(line)
	if fileName == "" {
		return ""
	}
	
	// 在文件树中搜索匹配的文件
	return findFilePathInTree(project.FileTree, fileName)
}

// 在文件树中搜索文件路径
func findFilePathInTree(node *FileNode, fileName string) string {
	if node == nil {
		return ""
	}
	
	// 检查当前节点
	if node.Name == fileName {
		return node.Path
	}
	
	// 递归搜索子节点
	if node.Children != nil {
		for _, child := range node.Children {
			if result := findFilePathInTree(child, fileName); result != "" {
				return result
			}
		}
	}
	
	return ""
}

// ========== 动态窗口大小调整功能 ==========

// 调整命令窗口高度 - 增加 (防崩溃加强版)
func adjustCommandHeightUp(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx == nil || ctx.Layout == nil {
			return nil
		}
		
		// 获取终端尺寸并检查有效性
		_, maxY := g.Size()
		if maxY <= 0 {
			return nil
		}
		
		// 安全的边界检查：增加命令窗口高度，最大不超过终端高度的60%
		maxHeight := maxY * 6 / 10  // 60% of terminal height
		if maxHeight < 5 {
			maxHeight = 5
		}
		// 更严格的限制：确保至少留给上半部分5行空间
		if maxHeight > maxY - 5 {
			maxHeight = maxY - 5
		}
		
		if ctx.Layout.CommandHeight < maxHeight && ctx.Layout.CommandHeight + 2 <= maxHeight {
			ctx.Layout.CommandHeight += 2
			
			// 🔧 额外安全检查：重新计算右侧面板分割点
			newAvailableHeight := maxY - ctx.Layout.CommandHeight
			if newAvailableHeight > 5 {
				ctx.Layout.RightPanelSplit1 = newAvailableHeight / 3
				ctx.Layout.RightPanelSplit2 = 2 * newAvailableHeight / 3
				
				// 确保分割点有效
				if ctx.Layout.RightPanelSplit1 < 2 {
					ctx.Layout.RightPanelSplit1 = 2
				}
				if ctx.Layout.RightPanelSplit2 <= ctx.Layout.RightPanelSplit1 + 1 {
					ctx.Layout.RightPanelSplit2 = ctx.Layout.RightPanelSplit1 + 2
				}
			}
			
			ctx.CommandDirty = true
		}
		return nil
	}
}

// 调整命令窗口高度 - 减少 (防崩溃加强版)
func adjustCommandHeightDown(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx == nil || ctx.Layout == nil {
			return nil
		}
		
		// 获取终端尺寸并检查有效性
		_, maxY := g.Size()
		if maxY <= 0 {
			return nil
		}
		
		// 安全的边界检查：减少命令窗口高度，最小为5行
		minHeight := 5
		if ctx.Layout.CommandHeight > minHeight {
			ctx.Layout.CommandHeight -= 2
			// 确保不会降到最小值以下
			if ctx.Layout.CommandHeight < minHeight {
				ctx.Layout.CommandHeight = minHeight
			}
			ctx.CommandDirty = true
		}
		return nil
	}
}

// 调整左侧面板宽度 - 减少（代码区域变大）(防崩溃加强版)
func adjustLeftPanelWidthDown(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx == nil || ctx.Layout == nil {
			return nil
		}
		
		// 获取终端尺寸并检查有效性
		maxX, _ := g.Size()
		if maxX <= 0 {
			return nil
		}
		
		// 安全的边界检查：减少左侧面板宽度，最小为15列
		minWidth := 15
		if ctx.Layout.LeftPanelWidth > minWidth {
			ctx.Layout.LeftPanelWidth -= 5
			// 确保不会降到最小值以下
			if ctx.Layout.LeftPanelWidth < minWidth {
				ctx.Layout.LeftPanelWidth = minWidth
			}
			ctx.CommandDirty = true
		}
		return nil
	}
}

// 调整左侧面板宽度 - 增加（代码区域变小）(防崩溃加强版)
func adjustLeftPanelWidthUp(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx == nil || ctx.Layout == nil {
			return nil
		}
		
		// 获取终端尺寸并检查有效性
		maxX, _ := g.Size()
		if maxX <= 0 {
			return nil
		}
		
		// 安全的边界检查：增加左侧面板宽度，最大不超过终端宽度的一半
		maxWidth := maxX / 2
		if maxWidth < 20 {
			maxWidth = 20
		}
		
		if ctx.Layout.LeftPanelWidth < maxWidth {
			ctx.Layout.LeftPanelWidth += 5
			ctx.CommandDirty = true
		}
		return nil
	}
}


