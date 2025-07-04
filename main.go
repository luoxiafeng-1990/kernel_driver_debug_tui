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
			fmt.Printf("错误: 无效的项目路径: %s\n", projectPath)
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
		CommandHistory: []string{"欢迎使用RISC-V内核调试器 TUI v2.0", "输入 'help' 查看可用命令"},
		CommandDirty: true,
		DebugMode:    "live",
		GUI:          g,
	}

	// 如果有命令行参数，自动打开项目
	if len(os.Args) > 1 {
		projectPath := os.Args[1]
		fileManager := NewFileManager(ctx)
		if err := fileManager.InitProject(projectPath); err != nil {
			ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("自动打开项目失败: %v", err))
		} else {
			ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("已自动打开项目: %s", projectPath))
		}
		ctx.CommandDirty = true
	}

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

	// 计算窗口位置
	leftWidth := ctx.Layout.LeftPanelWidth
	rightWidth := ctx.Layout.RightPanelWidth
	cmdHeight := ctx.Layout.CommandHeight
	
	middleWidth := maxX - leftWidth - rightWidth
	middleHeight := maxY - cmdHeight

	// 文件浏览器 (左侧)
	if v, err := g.SetView("files", 0, 0, leftWidth-1, middleHeight-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "文件浏览器"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
		v.SelFgColor = gocui.ColorBlack
	}

	// 代码窗口 (中间)
	if v, err := g.SetView("code", leftWidth, 0, leftWidth+middleWidth-1, middleHeight-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "代码视图"
		v.Wrap = false
		v.Highlight = true
		v.SelBgColor = gocui.ColorYellow
		v.SelFgColor = gocui.ColorBlack
	}

	// 右侧面板分割
	rightSplit1 := ctx.Layout.RightPanelSplit1
	rightSplit2 := ctx.Layout.RightPanelSplit2
	rightStart := leftWidth + middleWidth

	// 寄存器窗口 (右上)
	if v, err := g.SetView("registers", rightStart, 0, maxX-1, rightSplit1-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "寄存器"
		v.Wrap = false
	}

	// 变量窗口 (右中)
	if v, err := g.SetView("variables", rightStart, rightSplit1, maxX-1, rightSplit2-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "变量"
		v.Wrap = false
	}

	// 堆栈窗口 (右下)
	if v, err := g.SetView("stack", rightStart, rightSplit2, maxX-1, middleHeight-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "堆栈"
		v.Wrap = false
	}

	// 命令窗口 (底部) - 关键修复：设置为可编辑
	if v, err := g.SetView("command", 0, middleHeight, maxX-1, maxY-2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "命令"
		v.Editable = true  // 🔧 修复：设置为可编辑
		v.Wrap = true
		v.Autoscroll = true
		
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
	// Ctrl+J/K - 调整命令窗口高度
	if err := g.SetKeybinding("", gocui.KeyCtrlJ, gocui.ModNone, adjustCommandHeightDown(ctx)); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyCtrlK, gocui.ModNone, adjustCommandHeightUp(ctx)); err != nil {
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
	if err := g.SetKeybinding("files", gocui.KeyEnter, gocui.ModNone, handleFileSelection(ctx)); err != nil {
		log.Panicln(err)
	}
	
	// 文件夹展开/收缩
	if err := g.SetKeybinding("files", gocui.KeySpace, gocui.ModNone, handleFileToggle(ctx)); err != nil {
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

	// 滚动事件
	if err := g.SetKeybinding("", gocui.MouseWheelUp, gocui.ModNone, mouseScrollUpHandler); err != nil {
		log.Panicln(err)
	}

	if err := g.SetKeybinding("", gocui.MouseWheelDown, gocui.ModNone, mouseScrollDownHandler); err != nil {
		log.Panicln(err)
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
		
		popup := createPopupWindow(ctx, "help", "帮助", 80, 30, strings.Split(helpContent, "\n"))
		showPopupWindow(ctx, popup)
		
		return nil
	}
}

func nextViewHandler(g *gocui.Gui, v *gocui.View) error {
	views := []string{"files", "code", "registers", "variables", "stack", "command"}
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
			ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("上一帧失败: %v", err))
		} else {
			ctx.CommandHistory = append(ctx.CommandHistory, "已跳转到上一帧")
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
			ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("下一帧失败: %v", err))
		} else {
			ctx.CommandHistory = append(ctx.CommandHistory, "已跳转到下一帧")
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
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("切换目录失败: %v", err))
			}
		} else {
			// 尝试打开文件
			err := fileManager.OpenFile(filePath)
			if err != nil {
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("打开文件失败: %v", err))
			} else {
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("已打开文件: %s", filePath))
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
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("已移除第 %d 行的断点", actualLine))
			}
		} else {
			functionName := fmt.Sprintf("line_%d", actualLine)
			err := fileManager.AddBreakpoint(ctx.Project.CurrentFile, actualLine, functionName)
			if err == nil {
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("已在第 %d 行添加断点", actualLine))
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
		// 基本鼠标处理
		if v != nil {
			g.SetCurrentView(v.Name())
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
		ox, oy := v.Origin()
		if oy > 0 {
			v.SetOrigin(ox, oy-1)
		}
	}
	return nil
}

func mouseScrollDownHandler(g *gocui.Gui, v *gocui.View) error {
	if v != nil {
		ox, oy := v.Origin()
		v.SetOrigin(ox, oy+1)
	}
	return nil
}

// ========== 视图更新 ==========

func updateAllViews(g *gocui.Gui, ctx *DebuggerContext) {
	uiManager := NewUIManager(ctx, g)
	
	if v, err := g.View("files"); err == nil {
		uiManager.UpdateFileListView(v)
	}
	
	if v, err := g.View("code"); err == nil {
		uiManager.UpdateCodeView(v)
	}
	
	if v, err := g.View("registers"); err == nil {
		uiManager.UpdateRegistersView(v)
	}
	
	if v, err := g.View("variables"); err == nil {
		uiManager.UpdateVariablesView(v)
	}
	
	if v, err := g.View("stack"); err == nil {
		uiManager.UpdateStackView(v)
	}
	
	if v, err := g.View("command"); err == nil {
		uiManager.UpdateCommandView(v)
	}
	
	if v, err := g.View("status"); err == nil {
		uiManager.UpdateStatusView(v)
	}
}

// ========== 辅助函数 ==========

func initDynamicLayout(maxX, maxY int) *DynamicLayout {
	return &DynamicLayout{
		LeftPanelWidth:   maxX / 4,
		RightPanelWidth:  maxX / 3,
		CommandHeight:    8,
		RightPanelSplit1: (maxY - 8) / 3,
		RightPanelSplit2: 2 * (maxY - 8) / 3,
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

// 调整命令窗口高度 - 增加
func adjustCommandHeightUp(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx.Layout != nil {
			// 增加命令窗口高度，最大不超过终端高度的一半
			_, maxY := g.Size()
			if ctx.Layout.CommandHeight < maxY/2 {
				ctx.Layout.CommandHeight += 2
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("命令窗口高度: %d", ctx.Layout.CommandHeight))
				ctx.CommandDirty = true
			}
		}
		return nil
	}
}

// 调整命令窗口高度 - 减少
func adjustCommandHeightDown(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx.Layout != nil {
			// 减少命令窗口高度，最小为5行
			if ctx.Layout.CommandHeight > 5 {
				ctx.Layout.CommandHeight -= 2
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("命令窗口高度: %d", ctx.Layout.CommandHeight))
				ctx.CommandDirty = true
			}
		}
		return nil
	}
}

// 调整左侧面板宽度 - 减少（代码区域变大）
func adjustLeftPanelWidthDown(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx.Layout != nil {
			// 减少左侧面板宽度，最小为15列
			if ctx.Layout.LeftPanelWidth > 15 {
				ctx.Layout.LeftPanelWidth -= 5
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("左侧面板宽度: %d", ctx.Layout.LeftPanelWidth))
				ctx.CommandDirty = true
			}
		}
		return nil
	}
}

// 调整左侧面板宽度 - 增加（代码区域变小）
func adjustLeftPanelWidthUp(ctx *DebuggerContext) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if ctx.Layout != nil {
			// 增加左侧面板宽度，最大不超过终端宽度的一半
			maxX, _ := g.Size()
			if ctx.Layout.LeftPanelWidth < maxX/2 {
				ctx.Layout.LeftPanelWidth += 5
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("左侧面板宽度: %d", ctx.Layout.LeftPanelWidth))
				ctx.CommandDirty = true
			}
		}
		return nil
	}
}


