package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"github.com/jroimartin/gocui"
)

// ========== 事件处理器 ==========

// EventHandler 事件处理器结构体
type EventHandler struct {
	ctx *DebuggerContext
	gui *gocui.Gui
}

// NewEventHandler 创建新的事件处理器
func NewEventHandler(ctx *DebuggerContext, gui *gocui.Gui) *EventHandler {
	return &EventHandler{
		ctx: ctx,
		gui: gui,
	}
}

// ========== 全局变量 ==========
var (
	fileScroll, regScroll, varScroll, stackScroll, codeScroll, memScroll int
	fileBrowserLineMap []*FileNode // 记录文件浏览器每一行对应的FileNode
	fileBrowserDisplayLines []string // 记录显示的行内容，用于调试
	globalCtx *DebuggerContext
)

// ========== 基本控制事件处理器 ==========

// Quit 退出程序
func (eh *EventHandler) Quit() error {
	return gocui.ErrQuit
}

// NextViewHandler 下一个视图
func (eh *EventHandler) NextViewHandler() error {
	views := []string{"filebrowser", "code", "registers", "variables", "stack", "command"}
	current := ""
	if v := eh.gui.CurrentView(); v != nil {
		current = v.Name()
	}
	
	nextIndex := 0
	for i, name := range views {
		if name == current {
			nextIndex = (i + 1) % len(views)
			break
		}
	}
	
	_, err := eh.gui.SetCurrentView(views[nextIndex])
	return err
}

// PrevViewHandler 上一个视图
func (eh *EventHandler) PrevViewHandler() error {
	views := []string{"filebrowser", "code", "registers", "variables", "stack", "command"}
	current := ""
	if v := eh.gui.CurrentView(); v != nil {
		current = v.Name()
	}
	
	prevIndex := len(views) - 1
	for i, name := range views {
		if name == current {
			prevIndex = (i - 1 + len(views)) % len(views)
			break
		}
	}
	
	_, err := eh.gui.SetCurrentView(views[prevIndex])
	return err
}

// ========== 视图切换处理器 ==========

// SwitchToFileBrowser 切换到文件浏览器
func (eh *EventHandler) SwitchToFileBrowser() error {
	_, err := eh.gui.SetCurrentView("filebrowser")
	return err
}

// SwitchToRegisters 切换到寄存器视图
func (eh *EventHandler) SwitchToRegisters() error {
	_, err := eh.gui.SetCurrentView("registers")
	return err
}

// SwitchToVariables 切换到变量视图
func (eh *EventHandler) SwitchToVariables() error {
	_, err := eh.gui.SetCurrentView("variables")
	return err
}

// SwitchToStack 切换到堆栈视图
func (eh *EventHandler) SwitchToStack() error {
	_, err := eh.gui.SetCurrentView("stack")
	return err
}

// SwitchToCode 切换到代码视图
func (eh *EventHandler) SwitchToCode() error {
	_, err := eh.gui.SetCurrentView("code")
	return err
}

// SwitchToCommand 切换到命令视图
func (eh *EventHandler) SwitchToCommand() error {
	_, err := eh.gui.SetCurrentView("command")
	return err
}

// ========== 滚动处理器 ==========

// ScrollUpHandler 向上滚动
func (eh *EventHandler) ScrollUpHandler() error {
	if v := eh.gui.CurrentView(); v != nil {
		eh.scrollWindowByName(v.Name(), -1)
	}
	return nil
}

// ScrollDownHandler 向下滚动
func (eh *EventHandler) ScrollDownHandler() error {
	if v := eh.gui.CurrentView(); v != nil {
		eh.scrollWindowByName(v.Name(), 1)
	}
	return nil
}

// scrollWindowByName 按名称滚动窗口
func (eh *EventHandler) scrollWindowByName(name string, direction int) {
	switch name {
	case "filebrowser":
		fileScroll += direction
		if fileScroll < 0 {
			fileScroll = 0
		}
		// 强制更新文件浏览器视图
		eh.gui.Update(func(g *gocui.Gui) error {
			viewUpdater := NewViewUpdater(eh.ctx, g)
			viewUpdater.UpdateFileBrowserView(g, eh.ctx)
			return nil
		})
	case "registers":
		regScroll += direction
		if regScroll < 0 {
			regScroll = 0
		}
		// 强制更新寄存器视图
		eh.gui.Update(func(g *gocui.Gui) error {
			viewUpdater := NewViewUpdater(eh.ctx, g)
			viewUpdater.UpdateRegistersView(g, eh.ctx)
			return nil
		})
	case "variables":
		varScroll += direction
		if varScroll < 0 {
			varScroll = 0
		}
		// 强制更新变量视图
		eh.gui.Update(func(g *gocui.Gui) error {
			viewUpdater := NewViewUpdater(eh.ctx, g)
			viewUpdater.UpdateVariablesView(g, eh.ctx)
			return nil
		})
	case "stack":
		stackScroll += direction
		if stackScroll < 0 {
			stackScroll = 0
		}
		// 强制更新堆栈视图
		eh.gui.Update(func(g *gocui.Gui) error {
			viewUpdater := NewViewUpdater(eh.ctx, g)
			viewUpdater.UpdateStackView(g, eh.ctx)
			return nil
		})
	case "code":
		codeScroll += direction
		if codeScroll < 0 {
			codeScroll = 0
		}
		// 强制更新代码视图
		eh.gui.Update(func(g *gocui.Gui) error {
			viewUpdater := NewViewUpdater(eh.ctx, g)
			viewUpdater.UpdateCodeView(g, eh.ctx)
			return nil
		})
	case "command":
		// 命令窗口使用内置的滚动方式
		if v := eh.gui.CurrentView(); v != nil && v.Name() == "command" {
			ox, oy := v.Origin()
			if direction > 0 {
				v.SetOrigin(ox, oy+1)
			} else if oy > 0 {
				v.SetOrigin(ox, oy-1)
			}
		}
	}
}

// ========== 鼠标事件处理器 ==========

// MouseFocusHandler 鼠标焦点处理器
func (eh *EventHandler) MouseFocusHandler() error {
	// 检测鼠标点击位置对应的窗口
	windowName := eh.detectClickedWindow()
	if windowName != "" {
		// 设置焦点到被点击的窗口
		_, err := eh.gui.SetCurrentView(windowName)
		if err != nil {
			return err
		}
		
		// 强制更新显示以反映焦点变化
		eh.gui.Update(func(g *gocui.Gui) error {
			// 更新视图显示
			viewUpdater := NewViewUpdater(eh.ctx, g)
			viewUpdater.UpdateAllViews(g, eh.ctx)
			return nil
		})
		return nil
	}
	
	return nil
}

// MouseScrollUpHandler 鼠标向上滚动处理器
func (eh *EventHandler) MouseScrollUpHandler() error {
	return eh.ScrollUpHandler()
}

// MouseScrollDownHandler 鼠标向下滚动处理器
func (eh *EventHandler) MouseScrollDownHandler() error {
	return eh.ScrollDownHandler()
}

// MouseDownHandler 鼠标按下处理器
func (eh *EventHandler) MouseDownHandler() error {
	// 获取鼠标位置和当前视图
	currentView := eh.gui.CurrentView()
	if currentView == nil {
		return nil
	}

	// 检查是否点击在布局边界（用于调整窗口大小）
	if eh.ctx.Layout != nil {
		maxX, maxY := eh.gui.Size()
		cx, cy := currentView.Cursor()
		ox, oy := currentView.Origin()
		x, y := cx+ox, cy+oy

		layoutManager := NewLayoutManager(eh.ctx, eh.gui)
		boundary := layoutManager.DetectResizeBoundary(x, y, eh.ctx.Layout, maxX, maxY)
		if boundary != "" {
			layoutManager.StartDrag(boundary, x, y, eh.ctx.Layout)
			return nil
		}
	}

	// 处理弹出窗口点击
	popupManager := NewPopupManager(eh.ctx, eh.gui)
	if strings.HasPrefix(currentView.Name(), "popup_") {
		return popupManager.PopupMouseHandler(eh.gui, currentView)
	}

	// 处理不同视图的点击事件
	switch currentView.Name() {
	case "filebrowser":
		return eh.HandleFileBrowserClick()
	case "code":
		return eh.HandleCodeViewClick()
	case "command":
		return eh.HandleCommandClick()
	}

	return nil
}

// MouseDragResizeHandler 鼠标拖拽调整大小处理器
func (eh *EventHandler) MouseDragResizeHandler() error {
	if eh.ctx.Layout != nil && eh.ctx.Layout.IsDragging {
		currentView := eh.gui.CurrentView()
		if currentView != nil {
			maxX, maxY := eh.gui.Size()
			cx, cy := currentView.Cursor()
			ox, oy := currentView.Origin()
			x, y := cx+ox, cy+oy

			layoutManager := NewLayoutManager(eh.ctx, eh.gui)
			layoutManager.HandleDragMove(x, y, eh.ctx.Layout, maxX, maxY)
		}
	}
	return nil
}

// MouseUpHandler 鼠标松开处理器
func (eh *EventHandler) MouseUpHandler() error {
	if eh.ctx.Layout != nil && eh.ctx.Layout.IsDragging {
		layoutManager := NewLayoutManager(eh.ctx, eh.gui)
		layoutManager.EndDrag(eh.ctx.Layout)
	}
	return nil
}

// ========== 文件浏览器事件处理器 ==========

// HandleFileSelection 处理文件选择
func (eh *EventHandler) HandleFileSelection() error {
	v := eh.gui.CurrentView()
	if v == nil || eh.ctx.Project == nil {
		return nil
	}

	_, cy := v.Cursor()
	// 调整索引，考虑滚动偏移
	lineIndex := cy + fileScroll
	
	if lineIndex >= 0 && lineIndex < len(fileBrowserLineMap) && fileBrowserLineMap[lineIndex] != nil {
		node := fileBrowserLineMap[lineIndex]
		
		if node.IsDir {
			// 切换目录展开/收缩状态
			node.Expanded = !node.Expanded
			eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, 
				fmt.Sprintf("Toggled directory: %s", node.Name))
		} else {
			// 打开文件
			fileManager := NewFileManager(eh.ctx)
			err := fileManager.OpenFile(node.Path)
			if err != nil {
				eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, 
					fmt.Sprintf("Failed to open file: %v", err))
			} else {
				eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, 
					fmt.Sprintf("Opened file: %s", node.Name))
			}
		}
		eh.ctx.CommandDirty = true
	}

	return nil
}

// HandleFileBrowserClick 处理文件浏览器点击
func (eh *EventHandler) HandleFileBrowserClick() error {
	// 检测双击
	now := time.Now()
	v := eh.gui.CurrentView()
	if v == nil {
		return nil
	}
	
	_, cy := v.Cursor()
	
	if now.Sub(eh.ctx.LastClickTime) < 500*time.Millisecond && cy == eh.ctx.LastClickLine {
		// 双击：打开文件或切换目录
		return eh.HandleFileSelection()
	} else {
		// 单击：更新选择状态
		eh.ctx.LastClickTime = now
		eh.ctx.LastClickLine = cy
	}
	
	return nil
}

// ========== 代码视图事件处理器 ==========

// HandleBreakpointToggle 处理断点切换
func (eh *EventHandler) HandleBreakpointToggle() error {
	if eh.ctx.SearchMode {
		// 在搜索模式下，Enter键用于搜索
		return eh.HandleSearchEnter()
	}

	v := eh.gui.CurrentView()
	if v == nil || eh.ctx.Project == nil || eh.ctx.Project.CurrentFile == "" {
		return nil
	}

	_, cy := v.Cursor()
	lineNumber := cy + 1 + codeScroll // 转换为1基索引

	fileManager := NewFileManager(eh.ctx)
	fileManager.ToggleBreakpoint(eh.ctx.Project.CurrentFile, lineNumber)
	
	eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, 
		fmt.Sprintf("切换断点: %s:%d", eh.ctx.Project.CurrentFile, lineNumber))
	eh.ctx.CommandDirty = true

	return nil
}

// HandleCodeViewClick 处理代码视图点击
func (eh *EventHandler) HandleCodeViewClick() error {
	// 检测双击
	now := time.Now()
	v := eh.gui.CurrentView()
	if v == nil {
		return nil
	}
	
	_, cy := v.Cursor()
	
	if now.Sub(eh.ctx.LastClickTime) < 500*time.Millisecond && cy == eh.ctx.LastClickLine {
		// 双击：切换断点
		return eh.HandleBreakpointToggle()
	} else {
		// 单击：更新选择状态
		eh.ctx.LastClickTime = now
		eh.ctx.LastClickLine = cy
	}
	
	return nil
}

// ========== 命令输入事件处理器 ==========

// HandleCommand 处理命令输入
func (eh *EventHandler) HandleCommand() error {
	command := strings.TrimSpace(eh.ctx.CurrentInput)
	if command != "" {
		eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, fmt.Sprintf("> %s", command))
		
		// 执行命令
		uiManager := NewUIManager(eh.ctx, eh.gui)
		uiManager.ExecuteCommand(command)
		
		// 清空当前输入
		eh.ctx.CurrentInput = ""
	}
	eh.ctx.CommandDirty = true
	return nil
}

// HandleCharInput 处理字符输入
func (eh *EventHandler) HandleCharInput(ch rune) error {
	if eh.ctx.SearchMode {
		// 在搜索模式下，字符输入到搜索缓冲区
		eh.ctx.SearchInput += string(ch)
		eh.performSearch()
	} else {
		// 正常模式下，字符输入到命令缓冲区
		eh.ctx.CurrentInput += string(ch)
	}
	eh.ctx.CommandDirty = true
	return nil
}

// HandleBackspace 处理退格键
func (eh *EventHandler) HandleBackspace() error {
	if eh.ctx.SearchMode {
		// 在搜索模式下，从搜索缓冲区删除字符
		if len(eh.ctx.SearchInput) > 0 {
			eh.ctx.SearchInput = eh.ctx.SearchInput[:len(eh.ctx.SearchInput)-1]
			eh.performSearch()
		}
	} else {
		// 正常模式下，从命令缓冲区删除字符
		if len(eh.ctx.CurrentInput) > 0 {
			eh.ctx.CurrentInput = eh.ctx.CurrentInput[:len(eh.ctx.CurrentInput)-1]
		}
	}
	eh.ctx.CommandDirty = true
	return nil
}

// HandleCommandClick 处理命令窗口点击
func (eh *EventHandler) HandleCommandClick() error {
	// 设置焦点到命令窗口
	eh.gui.SetCurrentView("command")
	return nil
}

// ClearCurrentInput 清空当前输入
func (eh *EventHandler) ClearCurrentInput() error {
	eh.ctx.CurrentInput = ""
	eh.ctx.CommandDirty = true
	return nil
}

// ========== 搜索相关事件处理器 ==========

// StartSearchHandler 开始搜索处理器
func (eh *EventHandler) StartSearchHandler() error {
	eh.startSearchMode()
	return nil
}

// HandleSearchEnter 处理搜索回车键
func (eh *EventHandler) HandleSearchEnter() error {
	if eh.ctx.SearchMode && eh.ctx.SearchInput != "" {
		eh.ctx.SearchTerm = eh.ctx.SearchInput
		eh.performSearch()
		eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, 
			fmt.Sprintf("搜索: %s (找到%d个结果)", eh.ctx.SearchTerm, len(eh.ctx.SearchResults)))
		eh.ctx.CommandDirty = true
	}
	return nil
}

// HandleSearchEscape 处理搜索ESC键
func (eh *EventHandler) HandleSearchEscape() error {
	if eh.ctx.SearchMode {
		eh.exitSearchMode()
		return nil
	}
	// 如果不在搜索模式，由布局管理器处理ESC
	layoutManager := NewLayoutManager(eh.ctx, eh.gui)
	return layoutManager.EscapeExitFullscreen()
}

// JumpToPrevMatchHandler 跳转到上一个匹配项处理器
func (eh *EventHandler) JumpToPrevMatchHandler() error {
	eh.jumpToPrevMatch()
	return nil
}

// JumpToNextMatchHandler 跳转到下一个匹配项处理器
func (eh *EventHandler) JumpToNextMatchHandler() error {
	eh.jumpToNextMatch()
	return nil
}

// ========== BPF相关事件处理器 ==========

// GenerateBPFHandler 生成BPF处理器
func (eh *EventHandler) GenerateBPFHandler() error {
	if eh.ctx.Project == nil {
		eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, "Error: No project opened")
		eh.ctx.CommandDirty = true
		return nil
	}

	bpfGenerator := NewBPFCodeGenerator(eh.ctx)
	_, err := bpfGenerator.GenerateBPFProgram()
	if err != nil {
		eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, 
			fmt.Sprintf("Failed to generate BPF program: %v", err))
	} else {
		eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, "BPF program generated successfully")
		eh.ctx.BpfLoaded = true
	}
	eh.ctx.CommandDirty = true
	return nil
}

// ClearBreakpointsHandler 清除断点处理器
func (eh *EventHandler) ClearBreakpointsHandler() error {
	if eh.ctx.Project != nil {
		count := len(eh.ctx.Project.Breakpoints)
		eh.ctx.Project.Breakpoints = nil
		eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, 
			fmt.Sprintf("已清除%d个断点", count))
		eh.ctx.CommandDirty = true
	}
	return nil
}

// ========== 文本选择和复制 ==========

// SelectCurrentLine 选择当前行
func (eh *EventHandler) SelectCurrentLine() error {
	v := eh.gui.CurrentView()
	if v == nil {
		return nil
	}

	// 获取当前行内容
	_, cy := v.Cursor()
	line, err := v.Line(cy)
	if err != nil {
		return err
	}

	// 复制到剪贴板
	return eh.copyToClipboard(line)
}

// SelectWordAtCursor 选择光标处的单词
func (eh *EventHandler) SelectWordAtCursor() error {
	v := eh.gui.CurrentView()
	if v == nil {
		return nil
	}

	cx, cy := v.Cursor()
	line, err := v.Line(cy)
	if err != nil {
		return err
	}

	if cx >= len(line) {
		return nil
	}

	// 找到单词边界
	start := cx
	end := cx

	// 向前找单词开始
	for start > 0 && eh.isWordChar(line[start-1]) {
		start--
	}

	// 向后找单词结束
	for end < len(line) && eh.isWordChar(line[end]) {
		end++
	}

	if start < end {
		word := line[start:end]
		return eh.copyToClipboard(word)
	}

	return nil
}

// isWordChar 检查字符是否为单词字符
func (eh *EventHandler) isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || 
		   (c >= '0' && c <= '9') || c == '_'
}

// ClearSelection 清除选择
func (eh *EventHandler) ClearSelection() error {
	eh.ctx.SelectionMode = false
	eh.ctx.SelectionText = ""
	return nil
}

// copyToClipboard 复制到剪贴板
func (eh *EventHandler) copyToClipboard(text string) error {
	// 尝试使用系统剪贴板
	if err := eh.trySystemClipboard(text); err == nil {
		return nil
	}

	// 回退到OSC52
	return eh.copyWithOSC52(text)
}

// trySystemClipboard 尝试使用系统剪贴板
func (eh *EventHandler) trySystemClipboard(text string) error {
	// 尝试xclip
	cmd := exec.Command("xclip", "-selection", "clipboard")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err == nil {
		return nil
	}

	// 尝试xsel
	cmd = exec.Command("xsel", "--clipboard", "--input")
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err == nil {
		return nil
	}

	// 尝试pbcopy (macOS)
	cmd = exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}

// copyWithOSC52 使用OSC52序列复制
func (eh *EventHandler) copyWithOSC52(text string) error {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	osc52 := fmt.Sprintf("\033]52;c;%s\007", encoded)
	_, err := os.Stdout.WriteString(osc52)
	return err
}

// detectClickedWindow 检测鼠标点击位置对应的窗口
func (eh *EventHandler) detectClickedWindow() string {
	currentView := eh.gui.CurrentView()
	if currentView == nil {
		return ""
	}

	// 获取鼠标位置
	cx, cy := currentView.Cursor()
	ox, oy := currentView.Origin()
	x, y := cx+ox, cy+oy

	// 检测点击位置在哪个窗口内
	maxX, maxY := eh.gui.Size()
	
	if eh.ctx.Layout != nil {
		leftWidth := eh.ctx.Layout.LeftPanelWidth
		rightWidth := eh.ctx.Layout.RightPanelWidth
		cmdHeight := eh.ctx.Layout.CommandHeight
		middleWidth := maxX - leftWidth - rightWidth
		middleHeight := maxY - cmdHeight
		rightStart := leftWidth + middleWidth
		rightSplit1 := eh.ctx.Layout.RightPanelSplit1
		rightSplit2 := eh.ctx.Layout.RightPanelSplit2
		
		// 检测文件浏览器 (左侧)
		if x >= 0 && x < leftWidth && y >= 0 && y < middleHeight {
			return "filebrowser"
		}
		
		// 检测代码窗口 (中间)
		if x >= leftWidth && x < leftWidth+middleWidth && y >= 0 && y < middleHeight {
			return "code"
		}
		
		// 检测寄存器窗口 (右上)
		if x >= rightStart && x < maxX && y >= 0 && y < rightSplit1 {
			return "registers"
		}
		
		// 检测变量窗口 (右中)
		if x >= rightStart && x < maxX && y >= rightSplit1 && y < rightSplit2 {
			return "variables"
		}
		
		// 检测堆栈窗口 (右下)
		if x >= rightStart && x < maxX && y >= rightSplit2 && y < middleHeight {
			return "stack"
		}
		
		// 检测命令窗口 (底部)
		if x >= 0 && x < maxX && y >= middleHeight && y < maxY-2 {
			return "command"
		}
		
		// 检测状态栏 (最底部)
		if x >= 0 && x < maxX && y >= maxY-2 && y < maxY {
			return "status"
		}
	}
	
	// 如果无法检测到特定窗口，返回当前视图名称
	return currentView.Name()
}

// ========== 搜索功能实现 ==========

// startSearchMode 开始搜索模式
func (eh *EventHandler) startSearchMode() {
	eh.ctx.SearchMode = true
	eh.ctx.SearchInput = ""
	eh.ctx.SearchResults = nil
	eh.ctx.CurrentMatch = -1
	eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, "进入搜索模式 (输入搜索词，ESC退出)")
	eh.ctx.CommandDirty = true
}

// exitSearchMode 退出搜索模式
func (eh *EventHandler) exitSearchMode() {
	eh.ctx.SearchMode = false
	eh.ctx.SearchInput = ""
	eh.ctx.SearchResults = nil
	eh.ctx.CurrentMatch = -1
	eh.ctx.CommandHistory = append(eh.ctx.CommandHistory, "退出搜索模式")
	eh.ctx.CommandDirty = true
}

// performSearch 执行搜索
func (eh *EventHandler) performSearch() {
	if eh.ctx.Project == nil || eh.ctx.Project.CurrentFile == "" || eh.ctx.SearchInput == "" {
		eh.ctx.SearchResults = nil
		return
	}

	content, exists := eh.ctx.Project.OpenFiles[eh.ctx.Project.CurrentFile]
	if !exists {
		return
	}

	eh.ctx.SearchResults = nil
	eh.ctx.CurrentMatch = -1

	for lineNum, line := range content {
		if strings.Contains(strings.ToLower(line), strings.ToLower(eh.ctx.SearchInput)) {
			result := SearchResult{
				LineNumber:  lineNum + 1,
				StartColumn: strings.Index(strings.ToLower(line), strings.ToLower(eh.ctx.SearchInput)),
				EndColumn:   0,
				Text:        strings.TrimSpace(line),
			}
			result.EndColumn = result.StartColumn + len(eh.ctx.SearchInput)
			eh.ctx.SearchResults = append(eh.ctx.SearchResults, result)
		}
	}

	if len(eh.ctx.SearchResults) > 0 {
		eh.ctx.CurrentMatch = 0
	}

	eh.ctx.SearchDirty = true
}

// jumpToNextMatch 跳转到下一个匹配项
func (eh *EventHandler) jumpToNextMatch() {
	if len(eh.ctx.SearchResults) == 0 {
		return
	}

	eh.ctx.CurrentMatch = (eh.ctx.CurrentMatch + 1) % len(eh.ctx.SearchResults)
	
	// 更新代码视图滚动位置
	if eh.ctx.CurrentMatch >= 0 && eh.ctx.CurrentMatch < len(eh.ctx.SearchResults) {
		result := eh.ctx.SearchResults[eh.ctx.CurrentMatch]
		codeScroll = result.LineNumber - 5 // 让匹配行显示在中间
		if codeScroll < 0 {
			codeScroll = 0
		}
	}
}

// jumpToPrevMatch 跳转到上一个匹配项
func (eh *EventHandler) jumpToPrevMatch() {
	if len(eh.ctx.SearchResults) == 0 {
		return
	}

	eh.ctx.CurrentMatch = (eh.ctx.CurrentMatch - 1 + len(eh.ctx.SearchResults)) % len(eh.ctx.SearchResults)
	
	// 更新代码视图滚动位置
	if eh.ctx.CurrentMatch >= 0 && eh.ctx.CurrentMatch < len(eh.ctx.SearchResults) {
		result := eh.ctx.SearchResults[eh.ctx.CurrentMatch]
		codeScroll = result.LineNumber - 5 // 让匹配行显示在中间
		if codeScroll < 0 {
			codeScroll = 0
		}
	}
} 