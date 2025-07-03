package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"path/filepath"
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io/ioutil"

	"github.com/jroimartin/gocui"
)

// 调试器状态
const (
	DEBUG_STOPPED = iota
	DEBUG_RUNNING
	DEBUG_STEPPING
	DEBUG_BREAKPOINT
)

type DebuggerState int

// 文件节点结构
type FileNode struct {
	Name     string
	Path     string
	IsDir    bool
	Children []*FileNode
	Expanded bool
}

// 断点信息
type Breakpoint struct {
	File     string
	Line     int
	Function string
	Enabled  bool
}

// 项目信息
type ProjectInfo struct {
	RootPath    string
	FileTree    *FileNode
	OpenFiles   map[string][]string // 文件路径 -> 文件内容行数组
	CurrentFile string
	Breakpoints []Breakpoint
}

type DebuggerContext struct {
	State         DebuggerState
	CurrentFocus  int
	BpfLoaded     bool
	CurrentFunc   string
	CurrentAddr   uint64
	Running       bool
	MouseEnabled  bool
	// 文本选择状态
	SelectionMode bool
	SelectionView string
	SelectionText string
	// 鼠标选择状态
	MouseSelecting bool
	SelectStartX   int
	SelectStartY   int
	SelectEndX     int
	SelectEndY     int
	// 项目管理
	Project       *ProjectInfo
	// 动态布局支持
	Layout        *DynamicLayout
	// 命令窗口状态管理 - 类似终端的历史记录
	CommandHistory []string  // 保存所有命令历史（包括命令和输出）
	CurrentInput   string    // 当前正在输入的命令
	CommandDirty   bool      // 标记命令窗口是否需要重绘
	// 双击检测状态
	LastClickTime  time.Time // 上次点击时间
	LastClickLine  int       // 上次点击的行号
	// 全屏状态管理
	IsFullscreen   bool          // 是否处于全屏状态
	FullscreenView string        // 当前全屏的窗口名称
	SavedLayout    *DynamicLayout // 保存的原始布局
	// 弹出窗口系统
	PopupWindows   []*PopupWindow // 所有弹出窗口列表
	DraggingPopup  *PopupWindow  // 当前正在拖拽的弹出窗口
}

// 动态布局配置
type DynamicLayout struct {
	// 窗口边界位置 (可调整)
	LeftPanelWidth    int  // 左侧文件浏览器宽度
	RightPanelWidth   int  // 右侧面板宽度
	CommandHeight     int  // 命令窗口高度
	RightPanelSplit1  int  // 右侧面板第一个分割点 (寄存器/变量)
	RightPanelSplit2  int  // 右侧面板第二个分割点 (变量/堆栈)
	
	// 拖拽状态
	IsDragging        bool
	DragBoundary      string // "left", "right", "bottom", "right1", "right2"
	DragStartX        int
	DragStartY        int
	DragOriginalValue int
}

// 弹出窗口结构
type PopupWindow struct {
	ID         string   // 窗口唯一标识
	Title      string   // 窗口标题
	X, Y       int      // 窗口左上角位置
	Width      int      // 窗口宽度  
	Height     int      // 窗口高度
	Content    []string // 窗口内容（按行存储）
	Visible    bool     // 是否可见
	Dragging   bool     // 是否正在拖拽
	DragStartX int      // 拖拽起始X坐标
	DragStartY int      // 拖拽起始Y坐标
	ScrollY    int      // 垂直滚动偏移
}

var (
	focusNames = []string{"文件浏览器", "寄存器", "变量", "函数调用堆栈", "代码视图", "内存", "命令"}
	// 全局调试器上下文（原版gocui没有UserData字段）
	globalCtx *DebuggerContext
)

// ========== 窗口滚动状态 ==========
var (
	fileScroll, regScroll, varScroll, stackScroll, codeScroll, memScroll int
)

// ========== 文件浏览器行映射 ==========
var (
	fileBrowserLineMap []*FileNode // 记录文件浏览器每一行对应的FileNode
	fileBrowserDisplayLines []string // 记录显示的行内容，用于调试
)

// ========== 动态布局系统 ==========

// 全屏布局
func layoutFullscreen(g *gocui.Gui, viewName string, maxX, maxY int) error {
	// 状态栏始终显示
	if v, err := g.SetView("status", 0, 0, maxX-1, 2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "状态"
	}
	
	// 全屏窗口占据状态栏下方的所有空间
	if v, err := g.SetView(viewName, 0, 3, maxX-1, maxY-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
		
		// 根据窗口类型设置标题和属性
		switch viewName {
		case "filebrowser":
			v.Title = "文件浏览器 [全屏] - F11/ESC退出"
		case "code":
			v.Title = "代码视图 [全屏] - F11/ESC退出"
		case "registers":
			v.Title = "寄存器 [全屏] - F11/ESC退出"
		case "variables":
			v.Title = "变量 [全屏] - F11/ESC退出"
		case "stack":
			v.Title = "函数调用堆栈 [全屏] - F11/ESC退出"
		case "command":
			v.Title = "命令 [全屏] - F11/ESC退出"
			v.Editable = true
			v.Wrap = false
		default:
			v.Title = fmt.Sprintf("%s [全屏] - F11/ESC退出", viewName)
		}
	}
	
	// 隐藏其他所有窗口（通过将它们设置为不可见的大小）
	allViews := []string{"filebrowser", "code", "registers", "variables", "stack", "command"}
	for _, name := range allViews {
		if name != viewName {
			// 将其他窗口设置为不可见（位置在屏幕外）
			if _, err := g.SetView(name, maxX, maxY, maxX, maxY); err != nil && err != gocui.ErrUnknownView {
				return err
			}
		}
	}
	
	return nil
}

// 初始化动态布局
func initDynamicLayout(maxX, maxY int) *DynamicLayout {
	return &DynamicLayout{
		LeftPanelWidth:   35,                    // 左侧文件浏览器宽度
		RightPanelWidth:  35,                    // 右侧面板宽度
		CommandHeight:    5,                     // 命令窗口高度
		RightPanelSplit1: maxY / 3,             // 寄存器窗口底部
		RightPanelSplit2: 2 * maxY / 3,         // 变量窗口底部
		IsDragging:       false,
		DragBoundary:     "",
		DragStartX:       0,
		DragStartY:       0,
		DragOriginalValue: 0,
	}
}

// 检测鼠标是否在可拖拽边界上
func detectResizeBoundary(x, y int, layout *DynamicLayout, maxX, maxY int) string {
	tolerance := 1 // 边界检测容差
	
	// 检测左侧边界 (文件浏览器右边)
	if x >= layout.LeftPanelWidth-tolerance && x <= layout.LeftPanelWidth+tolerance && 
	   y >= 3 && y <= maxY-layout.CommandHeight {
		return "left"
	}
	
	// 检测右侧边界 (右侧面板左边)
	rightStart := maxX - layout.RightPanelWidth
	if x >= rightStart-tolerance && x <= rightStart+tolerance && 
	   y >= 3 && y <= maxY-layout.CommandHeight {
		return "right"
	}
	
	// 检测底部边界 (命令窗口上边)
	bottomStart := maxY - layout.CommandHeight
	if y >= bottomStart-tolerance && y <= bottomStart+tolerance && 
	   x >= 0 && x <= maxX-1 {
		return "bottom"
	}
	
	// 检测右侧面板内部分割线1 (寄存器/变量)
	if x >= rightStart && x <= maxX-1 && 
	   y >= layout.RightPanelSplit1-tolerance && y <= layout.RightPanelSplit1+tolerance {
		return "right1"
	}
	
	// 检测右侧面板内部分割线2 (变量/堆栈)
	if x >= rightStart && x <= maxX-1 && 
	   y >= layout.RightPanelSplit2-tolerance && y <= layout.RightPanelSplit2+tolerance {
		return "right2"
	}
	
	return ""
}

// 开始拖拽
func startDrag(boundary string, x, y int, layout *DynamicLayout) {
	layout.IsDragging = true
	layout.DragBoundary = boundary
	layout.DragStartX = x
	layout.DragStartY = y
	
	// 保存原始值
	switch boundary {
	case "left":
		layout.DragOriginalValue = layout.LeftPanelWidth
	case "right":
		layout.DragOriginalValue = layout.RightPanelWidth
	case "bottom":
		layout.DragOriginalValue = layout.CommandHeight
	case "right1":
		layout.DragOriginalValue = layout.RightPanelSplit1
	case "right2":
		layout.DragOriginalValue = layout.RightPanelSplit2
	}
}

// 处理拖拽移动
func handleDragMove(x, y int, layout *DynamicLayout, maxX, maxY int) {
	if !layout.IsDragging {
		return
	}
	
	switch layout.DragBoundary {
	case "left":
		// 左侧边界：调整文件浏览器宽度
		newWidth := layout.DragOriginalValue + (x - layout.DragStartX)
		if newWidth >= 20 && newWidth <= maxX-60 { // 最小20，为代码和右侧面板留60
			layout.LeftPanelWidth = newWidth
		}
		
	case "right":
		// 右侧边界：调整右侧面板宽度
		deltaX := layout.DragStartX - x // 向左拖拽为正
		newWidth := layout.DragOriginalValue + deltaX
		if newWidth >= 25 && newWidth <= maxX-40 { // 最小25，为左侧和代码留40
			layout.RightPanelWidth = newWidth
		}
		
	case "bottom":
		// 底部边界：调整命令窗口高度
		deltaY := layout.DragStartY - y // 向上拖拽为正
		newHeight := layout.DragOriginalValue + deltaY
		if newHeight >= 3 && newHeight <= maxY/2 { // 最小3行，最大屏幕一半
			layout.CommandHeight = newHeight
		}
		
	case "right1":
		// 右侧面板分割线1：调整寄存器窗口高度
		newSplit := layout.DragOriginalValue + (y - layout.DragStartY)
		bottomLimit := maxY - layout.CommandHeight - 6 // 为变量和堆栈窗口留空间
		if newSplit >= 6 && newSplit <= bottomLimit && newSplit < layout.RightPanelSplit2-3 {
			layout.RightPanelSplit1 = newSplit
		}
		
	case "right2":
		// 右侧面板分割线2：调整变量窗口高度
		newSplit := layout.DragOriginalValue + (y - layout.DragStartY)
		bottomLimit := maxY - layout.CommandHeight - 3 // 为堆栈窗口留空间
		if newSplit >= layout.RightPanelSplit1+3 && newSplit <= bottomLimit {
			layout.RightPanelSplit2 = newSplit
		}
	}
}

// 结束拖拽
func endDrag(layout *DynamicLayout) {
	layout.IsDragging = false
	layout.DragBoundary = ""
}

// 重置布局到默认值
func resetLayout(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil {
		return nil
	}
	
	maxX, maxY := g.Size()
	globalCtx.Layout = initDynamicLayout(maxX, maxY)
	
	return nil
}

// F11全屏切换处理函数
func toggleFullscreenHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil {
		return nil
	}
	
	if globalCtx.IsFullscreen {
		// 退出全屏：恢复之前的布局
		if globalCtx.SavedLayout != nil {
			globalCtx.Layout = globalCtx.SavedLayout
			globalCtx.SavedLayout = nil
		}
		globalCtx.IsFullscreen = false
		globalCtx.FullscreenView = ""
		
		// 重新聚焦到之前的窗口
		if v != nil {
			g.SetCurrentView(v.Name())
		}
		
	} else {
		// 进入全屏：保存当前布局
		currentView := g.CurrentView()
		if currentView == nil {
			// 如果没有当前视图，默认全屏代码视图
			globalCtx.FullscreenView = "code"
		} else {
			viewName := currentView.Name()
			// 检查是否是有效的可全屏窗口
			validViews := []string{"filebrowser", "code", "registers", "variables", "stack", "command"}
			isValid := false
			for _, name := range validViews {
				if name == viewName {
					isValid = true
					break
				}
			}
			
			if isValid {
				globalCtx.FullscreenView = viewName
			} else {
				// 如果当前窗口不支持全屏，默认使用代码视图
				globalCtx.FullscreenView = "code"
			}
		}
		
		// 保存当前布局
		if globalCtx.Layout != nil {
			// 深拷贝当前布局
			globalCtx.SavedLayout = &DynamicLayout{
				LeftPanelWidth:    globalCtx.Layout.LeftPanelWidth,
				RightPanelWidth:   globalCtx.Layout.RightPanelWidth,
				CommandHeight:     globalCtx.Layout.CommandHeight,
				RightPanelSplit1:  globalCtx.Layout.RightPanelSplit1,
				RightPanelSplit2:  globalCtx.Layout.RightPanelSplit2,
				IsDragging:        false, // 重置拖拽状态
				DragBoundary:      "",
				DragStartX:        0,
				DragStartY:        0,
				DragOriginalValue: 0,
			}
		}
		
		globalCtx.IsFullscreen = true
		
		// 聚焦到全屏窗口
		g.SetCurrentView(globalCtx.FullscreenView)
	}
	
	return nil
}

// ESC键退出全屏处理函数
func escapeExitFullscreenHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil {
		return nil
	}
	
	// 添加调试信息到命令历史
	currentView := "none"
	if v != nil {
		currentView = v.Name()
	}
	
	// 首先检查当前视图是否是弹出窗口
	if v != nil && strings.HasPrefix(v.Name(), "popup_") {
		// 如果当前聚焦的是弹出窗口，直接关闭它
		popupID := strings.TrimPrefix(v.Name(), "popup_")
		if err := closePopupWindowWithView(g, globalCtx, popupID); err != nil {
			debugMsg := fmt.Sprintf("[ERROR] ESC键关闭当前弹出窗口失败: %s, 错误: %v", popupID, err)
			globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		} else {
			debugMsg := fmt.Sprintf("[DEBUG] ESC键成功关闭当前弹出窗口: %s", popupID)
			globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		}
		globalCtx.CommandDirty = true
		return nil
	}
	
	// 其次检查是否有弹出窗口需要关闭（处理其他情况）
	if len(globalCtx.PopupWindows) > 0 {
		// 关闭最顶层的弹出窗口
		lastPopup := globalCtx.PopupWindows[len(globalCtx.PopupWindows)-1]
		if err := closePopupWindowWithView(g, globalCtx, lastPopup.ID); err != nil {
			// 如果关闭失败，记录错误信息
			debugMsg := fmt.Sprintf("[ERROR] ESC键关闭弹出窗口失败: %s, 错误: %v", lastPopup.ID, err)
			globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		} else {
			// 调试信息
			debugMsg := fmt.Sprintf("[DEBUG] ESC键成功关闭弹出窗口: %s", lastPopup.ID)
			globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		}
		globalCtx.CommandDirty = true
		
		return nil
	}
	
	// 只有在全屏状态下才处理ESC键退出全屏
	if globalCtx.IsFullscreen {
		// 调试信息
		debugMsg := fmt.Sprintf("[DEBUG] ESC键退出全屏: 当前视图=%s, 全屏视图=%s", currentView, globalCtx.FullscreenView)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		globalCtx.CommandDirty = true
		
		// 退出全屏：恢复之前的布局
		if globalCtx.SavedLayout != nil {
			globalCtx.Layout = globalCtx.SavedLayout
			globalCtx.SavedLayout = nil
		}
		globalCtx.IsFullscreen = false
		
		// 保存当前全屏的窗口名称，用于重新聚焦
		previousView := globalCtx.FullscreenView
		globalCtx.FullscreenView = ""
		
		// 重新聚焦到之前的窗口
		if previousView != "" {
			g.SetCurrentView(previousView)
		}
		
		return nil
	}
	
	// 如果不在全屏状态，ESC键保持原有功能（如清空命令输入）
	// 检查当前是否在命令窗口
	if v != nil && v.Name() == "command" {
		// 调试信息
		debugMsg := fmt.Sprintf("[DEBUG] ESC键清空命令输入: 当前输入=%s", globalCtx.CurrentInput)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		globalCtx.CommandDirty = true
		
		return clearCurrentInput(g, v)
	}
	
	// 其他情况的调试信息
	debugMsg := fmt.Sprintf("[DEBUG] ESC键无操作: 视图=%s, 全屏状态=%v", currentView, globalCtx.IsFullscreen)
	globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
	globalCtx.CommandDirty = true
	
	return nil
}

// 键盘调整窗口大小
func adjustLeftPanelHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Layout == nil {
		return nil
	}
	
	maxX, _ := g.Size()
	newWidth := globalCtx.Layout.LeftPanelWidth + 5
	if newWidth <= maxX-60 {
		globalCtx.Layout.LeftPanelWidth = newWidth
	}
	
	return nil
}

func shrinkLeftPanelHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Layout == nil {
		return nil
	}
	
	newWidth := globalCtx.Layout.LeftPanelWidth - 5
	if newWidth >= 20 {
		globalCtx.Layout.LeftPanelWidth = newWidth
	}
	
	return nil
}

func adjustCommandHeightHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Layout == nil {
		return nil
	}
	
	_, maxY := g.Size()
	newHeight := globalCtx.Layout.CommandHeight + 2
	
	// 修复：添加commandStartY的下边界检查
	// 确保commandStartY >= 4，为状态栏(3行)和其他窗口留出最小空间
	commandStartY := maxY - newHeight
	if newHeight <= maxY/2 && commandStartY >= 4 {
		globalCtx.Layout.CommandHeight = newHeight
	}
	
	return nil
}

func shrinkCommandHeightHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Layout == nil {
		return nil
	}
	
	newHeight := globalCtx.Layout.CommandHeight - 2
	if newHeight >= 3 {
		globalCtx.Layout.CommandHeight = newHeight
	}
	
	return nil
}

func layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	
	// 检查是否处于全屏状态
	if globalCtx != nil && globalCtx.IsFullscreen && globalCtx.FullscreenView != "" {
		return layoutFullscreen(g, globalCtx.FullscreenView, maxX, maxY)
	}
	
	// 初始化动态布局（如果不存在）
	if globalCtx != nil && globalCtx.Layout == nil {
		globalCtx.Layout = initDynamicLayout(maxX, maxY)
	}
	
	// 获取布局参数
	var layout *DynamicLayout
	if globalCtx != nil && globalCtx.Layout != nil {
		layout = globalCtx.Layout
	} else {
		// 使用默认布局
		layout = initDynamicLayout(maxX, maxY)
	}
	
	// 修复：添加全面的边界检查和约束
	// 确保CommandHeight不会导致其他窗口坐标异常
	minCommandHeight := 3
	maxCommandHeight := maxY - 7  // 为状态栏(3行)和其他窗口(至少4行)留空间
	if maxCommandHeight < minCommandHeight {
		maxCommandHeight = minCommandHeight
	}
	
	if layout.CommandHeight < minCommandHeight {
		layout.CommandHeight = minCommandHeight
	}
	if layout.CommandHeight > maxCommandHeight {
		layout.CommandHeight = maxCommandHeight
	}
	
	// 计算安全的窗口底部坐标
	safeBottomY := maxY - layout.CommandHeight - 1
	if safeBottomY < 4 {
		safeBottomY = 4
		layout.CommandHeight = maxY - safeBottomY - 1
	}
	
	// 状态栏
	if v, err := g.SetView("status", 0, 0, maxX-1, 2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "状态"
	}
	
	// 文件浏览器窗口 (左侧) - 使用安全的底部坐标
	if v, err := g.SetView("filebrowser", 0, 3, layout.LeftPanelWidth, safeBottomY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "文件浏览器"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 代码窗口 (中央) - 使用安全的底部坐标
	codeStartX := layout.LeftPanelWidth + 1
	codeEndX := maxX - layout.RightPanelWidth - 1
	// 确保代码窗口有最小宽度
	if codeEndX <= codeStartX {
		codeEndX = codeStartX + 10
	}
	if v, err := g.SetView("code", codeStartX, 3, codeEndX, safeBottomY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "代码视图"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 右侧面板起始位置
	rightStartX := maxX - layout.RightPanelWidth
	
	// 确保右侧分割点在合理范围内
	minSplit1 := 6
	maxSplit1 := safeBottomY - 6
	if layout.RightPanelSplit1 < minSplit1 {
		layout.RightPanelSplit1 = minSplit1
	}
	if layout.RightPanelSplit1 > maxSplit1 {
		layout.RightPanelSplit1 = maxSplit1
	}
	
	minSplit2 := layout.RightPanelSplit1 + 3
	maxSplit2 := safeBottomY - 3
	if layout.RightPanelSplit2 < minSplit2 {
		layout.RightPanelSplit2 = minSplit2
	}
	if layout.RightPanelSplit2 > maxSplit2 {
		layout.RightPanelSplit2 = maxSplit2
	}
	
	// 寄存器窗口 (右上) - 使用安全的分割点
	if v, err := g.SetView("registers", rightStartX, 3, maxX-1, layout.RightPanelSplit1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "寄存器"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 变量窗口 (右中) - 使用安全的分割点
	if v, err := g.SetView("variables", rightStartX, layout.RightPanelSplit1+1, maxX-1, layout.RightPanelSplit2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "变量"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 调用栈窗口 (右下) - 使用安全的底部坐标
	if v, err := g.SetView("stack", rightStartX, layout.RightPanelSplit2+1, maxX-1, safeBottomY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "函数调用堆栈"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 命令窗口 (底部) - 使用安全的起始坐标
	commandStartY := safeBottomY + 1
	if commandStartY >= maxY {
		commandStartY = maxY - 2
	}
	
	if v, err := g.SetView("command", 0, commandStartY, maxX-1, maxY-1); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "命令"
		v.Editable = true
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
		v.Wrap = false       // 禁用自动换行，防止长文本被截断
	}
	
	// 渲染弹出窗口 (在最后渲染，确保在顶层显示)
	if err := renderPopupWindows(g, globalCtx); err != nil {
		return err
	}
	
	return nil
}

func quit(g *gocui.Gui, v *gocui.View) error {
	return gocui.ErrQuit
}

// ========== 项目管理功能 ==========

// 打开项目目录
func openProject(projectPath string) (*ProjectInfo, error) {
	// 检查目录是否存在
	if _, err := os.Stat(projectPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("项目目录不存在: %s", projectPath)
	}
	
	// 创建项目信息
	project := &ProjectInfo{
		RootPath:    projectPath,
		OpenFiles:   make(map[string][]string),
		Breakpoints: make([]Breakpoint, 0),
	}
	
	// 构建文件树
	fileTree, err := buildFileTree(projectPath)
	if err != nil {
		return nil, fmt.Errorf("构建文件树失败: %v", err)
	}
	project.FileTree = fileTree
	
	// 创建临时上下文以加载断点
	tempCtx := &DebuggerContext{Project: project}
	
	// 尝试加载保存的断点
	if err := loadBreakpoints(tempCtx); err != nil {
		// 如果加载断点失败，记录错误但不影响项目打开
		log.Printf("警告: 加载断点失败: %v", err)
	}
	
	return project, nil
}

// 构建文件树
func buildFileTree(rootPath string) (*FileNode, error) {
	info, err := os.Stat(rootPath)
	if err != nil {
		return nil, err
	}
	
	root := &FileNode{
		Name:     filepath.Base(rootPath),
		Path:     rootPath,
		IsDir:    info.IsDir(),
		Children: make([]*FileNode, 0),
		Expanded: true, // 根目录默认展开
	}
	
	if root.IsDir {
		// 使用简化的目录遍历，避免卡死 (Go 1.13兼容)
		files, err := ioutil.ReadDir(rootPath)
		if err != nil {
			return root, nil // 返回空的根节点而不是错误
		}
		
		// 限制文件数量，避免处理太多文件
		count := 0
		maxFiles := 100
		
		for _, file := range files {
			if count >= maxFiles {
				break
			}
			
			// 跳过隐藏文件
			if strings.HasPrefix(file.Name(), ".") {
				continue
			}
			
			fullPath := filepath.Join(rootPath, file.Name())
			
			// 如果是目录，添加但不递归
			if file.IsDir() {
				node := &FileNode{
					Name:     file.Name(),
					Path:     fullPath,
					IsDir:    true,
					Children: make([]*FileNode, 0),
					Expanded: false,
				}
				root.Children = append(root.Children, node)
				count++
			} else {
				// 只处理C/C++源文件和头文件
				ext := strings.ToLower(filepath.Ext(file.Name()))
				if ext == ".c" || ext == ".cpp" || ext == ".h" || ext == ".hpp" {
					node := &FileNode{
						Name:     file.Name(),
						Path:     fullPath,
						IsDir:    false,
						Children: make([]*FileNode, 0),
						Expanded: false,
					}
					root.Children = append(root.Children, node)
					count++
				}
			}
		}
	}
	
	return root, nil
}

// 读取文件内容
func readFileContent(filePath string) ([]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	
	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	
	return lines, nil
}

// 添加断点
func addBreakpoint(ctx *DebuggerContext, file string, line int) {
	if ctx.Project == nil {
		return
	}
	
	// 检查断点是否已存在
	for i, bp := range ctx.Project.Breakpoints {
		if bp.File == file && bp.Line == line {
			ctx.Project.Breakpoints[i].Enabled = !ctx.Project.Breakpoints[i].Enabled
			// 保存断点到文件
			if err := saveBreakpoints(ctx); err != nil {
				// 在命令历史中记录错误
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("[ERROR] 保存断点失败: %v", err))
				ctx.CommandDirty = true
			}
			return
		}
	}
	
	// 添加新断点
	bp := Breakpoint{
		File:     file,
		Line:     line,
		Function: "unknown", // 后续可以通过解析源码获取函数名
		Enabled:  true,
	}
	ctx.Project.Breakpoints = append(ctx.Project.Breakpoints, bp)
	
	// 保存断点到文件
	if err := saveBreakpoints(ctx); err != nil {
		// 在命令历史中记录错误
		ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("[ERROR] 保存断点失败: %v", err))
		ctx.CommandDirty = true
	}
}

// 生成BPF代码
func generateBPF(ctx *DebuggerContext) error {
	if ctx.Project == nil || len(ctx.Project.Breakpoints) == 0 {
		return fmt.Errorf("没有设置断点")
	}
	
	// 创建BPF文件
	bpfPath := filepath.Join(ctx.Project.RootPath, "debug_breakpoints.bpf.c")
	file, err := os.Create(bpfPath)
	if err != nil {
		return fmt.Errorf("创建BPF文件失败: %v", err)
	}
	defer file.Close()
	
	// 写入BPF代码头部
	fmt.Fprintln(file, "#include <linux/bpf.h>")
	fmt.Fprintln(file, "#include <bpf/bpf_helpers.h>")
	fmt.Fprintln(file, "#include <bpf/bpf_tracing.h>")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "// 自动生成的BPF调试代码")
	fmt.Fprintln(file, "")
	
	// 为每个断点生成探针
	for i, bp := range ctx.Project.Breakpoints {
		if !bp.Enabled {
			continue
		}
		
		fmt.Fprintf(file, "SEC(\"kprobe/%s\")\n", bp.Function)
		fmt.Fprintf(file, "int trace_breakpoint_%d(struct pt_regs *ctx) {\n", i)
		fmt.Fprintf(file, "    bpf_printk(\"断点触发: %s:%d\\n\");\n", bp.File, bp.Line)
		fmt.Fprintf(file, "    return 0;\n")
		fmt.Fprintf(file, "}\n\n")
	}
	
	fmt.Fprintln(file, "char LICENSE[] SEC(\"license\") = \"GPL\";")
	
	return nil
}

// ========== 弹出窗口系统 ==========

// 创建弹出窗口
func createPopupWindow(ctx *DebuggerContext, id, title string, width, height int, content []string) *PopupWindow {
	// 计算窗口居中位置 (假设屏幕80x24，实际会在layout时调整)
	x := (80 - width) / 2
	y := (24 - height) / 2
	if x < 0 { x = 0 }
	if y < 0 { y = 0 }
	
	popup := &PopupWindow{
		ID:       id,
		Title:    title,
		X:        x,
		Y:        y,
		Width:    width,
		Height:   height,
		Content:  content,
		Visible:  true,
		Dragging: false,
		ScrollY:  0,
	}
	
	return popup
}

// 显示弹出窗口
func showPopupWindow(ctx *DebuggerContext, popup *PopupWindow) {
	if ctx == nil {
		return
	}
	
	// 检查是否已存在相同ID的窗口
	for i, existing := range ctx.PopupWindows {
		if existing.ID == popup.ID {
			// 更新现有窗口
			ctx.PopupWindows[i] = popup
			return
		}
	}
	
	// 添加新窗口
	ctx.PopupWindows = append(ctx.PopupWindows, popup)
}

// 关闭弹出窗口
func closePopupWindow(ctx *DebuggerContext, id string) {
	if ctx == nil {
		return
	}
	
	for i, popup := range ctx.PopupWindows {
		if popup.ID == id {
			// 从切片中删除
			ctx.PopupWindows = append(ctx.PopupWindows[:i], ctx.PopupWindows[i+1:]...)
			break
		}
	}
}

// 关闭弹出窗口并删除gocui视图
func closePopupWindowWithView(g *gocui.Gui, ctx *DebuggerContext, id string) error {
	if ctx == nil {
		return nil
	}
	
	// 删除对应的gocui视图
	viewName := fmt.Sprintf("popup_%s", id)
	if err := g.DeleteView(viewName); err != nil && err != gocui.ErrUnknownView {
		// 如果删除视图失败且不是因为视图不存在，记录错误但继续
		log.Printf("警告: 删除弹出窗口视图失败: %v", err)
	}
	
	// 从弹出窗口列表中删除
	for i, popup := range ctx.PopupWindows {
		if popup.ID == id {
			// 如果正在拖拽这个窗口，停止拖拽
			if ctx.DraggingPopup != nil && ctx.DraggingPopup.ID == id {
				ctx.DraggingPopup = nil
			}
			
			// 从切片中删除
			ctx.PopupWindows = append(ctx.PopupWindows[:i], ctx.PopupWindows[i+1:]...)
			break
		}
	}
	
	return nil
}

// 查找弹出窗口
func findPopupWindow(ctx *DebuggerContext, id string) *PopupWindow {
	if ctx == nil {
		return nil
	}
	
	for _, popup := range ctx.PopupWindows {
		if popup.ID == id {
			return popup
		}
	}
	return nil
}

// 检测鼠标是否在弹出窗口内
func getPopupWindowAt(ctx *DebuggerContext, x, y int) *PopupWindow {
	if ctx == nil {
		return nil
	}
	
	// 从后往前检查 (后添加的窗口在顶层)
	for i := len(ctx.PopupWindows) - 1; i >= 0; i-- {
		popup := ctx.PopupWindows[i]
		if !popup.Visible {
			continue
		}
		
		if x >= popup.X && x < popup.X+popup.Width &&
		   y >= popup.Y && y < popup.Y+popup.Height {
			return popup
		}
	}
	return nil
}

// 检测鼠标是否在弹出窗口标题栏内
func isInPopupTitleBar(popup *PopupWindow, x, y int) bool {
	if popup == nil {
		return false
	}
	
	// 标题栏是窗口顶部的第一行
	return x >= popup.X && x < popup.X+popup.Width &&
		   y == popup.Y
}

// 弹出窗口专用关闭处理函数
func popupCloseHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil || globalCtx == nil {
		return nil
	}
	
	// 获取弹出窗口ID
	viewName := v.Name()
	if !strings.HasPrefix(viewName, "popup_") {
		return nil
	}
	popupID := strings.TrimPrefix(viewName, "popup_")
	
	// 关闭弹出窗口
	if err := closePopupWindowWithView(g, globalCtx, popupID); err != nil {
		debugMsg := fmt.Sprintf("[ERROR] q键关闭弹出窗口失败: %s, 错误: %v", popupID, err)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
	} else {
		debugMsg := fmt.Sprintf("[DEBUG] q键成功关闭弹出窗口: %s", popupID)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
	}
	globalCtx.CommandDirty = true
	
	return nil
}

// 为弹出窗口绑定鼠标事件和键盘事件
func bindPopupMouseEvents(g *gocui.Gui, viewName string) {
	// 绑定鼠标左键点击事件（用于拖拽）
	g.SetKeybinding(viewName, gocui.MouseLeft, gocui.ModNone, popupMouseHandler)
	
	// 绑定鼠标滚轮事件（用于滚动）
	g.SetKeybinding(viewName, gocui.MouseWheelUp, gocui.ModNone, popupScrollUpHandler)
	g.SetKeybinding(viewName, gocui.MouseWheelDown, gocui.ModNone, popupScrollDownHandler)
	
	// 绑定q键关闭弹出窗口（避免与全局ESC键冲突）
	g.SetKeybinding(viewName, 'q', gocui.ModNone, popupCloseHandler)
	g.SetKeybinding(viewName, 'Q', gocui.ModNone, popupCloseHandler)
	
	// 为了兼容，也绑定ESC键，但优先级较低
	g.SetKeybinding(viewName, gocui.KeyEsc, gocui.ModNone, popupCloseHandler)
	
	// 绑定方向键用于滚动
	g.SetKeybinding(viewName, gocui.KeyArrowUp, gocui.ModNone, popupScrollUpHandler)
	g.SetKeybinding(viewName, gocui.KeyArrowDown, gocui.ModNone, popupScrollDownHandler)
	
	// 注意：拖拽移动事件由全局的mouseDragResizeHandler处理
	// 鼠标释放事件由全局的mouseUpHandler处理
}

// 弹出窗口鼠标点击处理函数
func popupMouseHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil || globalCtx == nil {
		return nil
	}
	
	// 聚焦到弹出窗口
	g.SetCurrentView(v.Name())
	
	// 获取弹出窗口ID
	viewName := v.Name()
	if !strings.HasPrefix(viewName, "popup_") {
		return nil
	}
	popupID := strings.TrimPrefix(viewName, "popup_")
	
	// 查找对应的弹出窗口
	popup := findPopupWindow(globalCtx, popupID)
	if popup == nil {
		return nil
	}
	
	// 获取鼠标相对位置（简化实现）
	ox, oy := v.Origin()
	cx, cy := v.Cursor()
	mouseX := ox + cx
	mouseY := oy + cy
	
	// 检查是否点击了标题栏（用于拖拽）
	if isInPopupTitleBar(popup, mouseX, mouseY) {
		// 开始拖拽弹出窗口
		popup.Dragging = true
		popup.DragStartX = mouseX - popup.X
		popup.DragStartY = mouseY - popup.Y
		globalCtx.DraggingPopup = popup
		
		// 将此窗口移到最前面
		for i, p := range globalCtx.PopupWindows {
			if p.ID == popup.ID {
				// 移除当前位置的窗口
				globalCtx.PopupWindows = append(globalCtx.PopupWindows[:i], globalCtx.PopupWindows[i+1:]...)
				// 添加到末尾（最前面）
				globalCtx.PopupWindows = append(globalCtx.PopupWindows, popup)
				break
			}
		}
	}
	
	return nil
}

// 弹出窗口向上滚动处理函数
func popupScrollUpHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil || globalCtx == nil {
		return nil
	}
	
	// 获取弹出窗口ID
	viewName := v.Name()
	if !strings.HasPrefix(viewName, "popup_") {
		return nil
	}
	popupID := strings.TrimPrefix(viewName, "popup_")
	
	// 查找对应的弹出窗口
	popup := findPopupWindow(globalCtx, popupID)
	if popup == nil {
		return nil
	}
	
	// 向上滚动
	if popup.ScrollY > 0 {
		popup.ScrollY--
	}
	
	return nil
}

// 弹出窗口向下滚动处理函数
func popupScrollDownHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil || globalCtx == nil {
		return nil
	}
	
	// 获取弹出窗口ID
	viewName := v.Name()
	if !strings.HasPrefix(viewName, "popup_") {
		return nil
	}
	popupID := strings.TrimPrefix(viewName, "popup_")
	
	// 查找对应的弹出窗口
	popup := findPopupWindow(globalCtx, popupID)
	if popup == nil {
		return nil
	}
	
	// 向下滚动（检查是否还有更多内容）
	availableLines := popup.Height - 3 // 减去边框和提示行
	if availableLines < 1 {
		availableLines = 1
	}
	
	maxScroll := len(popup.Content) - availableLines
	if maxScroll < 0 {
		maxScroll = 0
	}
	
	if popup.ScrollY < maxScroll {
		popup.ScrollY++
	}
	
	return nil
}

// 渲染弹出窗口
func renderPopupWindows(g *gocui.Gui, ctx *DebuggerContext) error {
	if ctx == nil {
		return nil
	}
	
	maxX, maxY := g.Size()
	
	for i, popup := range ctx.PopupWindows {
		if !popup.Visible {
			continue
		}
		
		// 调整窗口位置以适应屏幕大小
		if popup.X + popup.Width > maxX {
			popup.X = maxX - popup.Width
		}
		if popup.Y + popup.Height > maxY {
			popup.Y = maxY - popup.Height
		}
		if popup.X < 0 { popup.X = 0 }
		if popup.Y < 0 { popup.Y = 0 }
		
		// 创建窗口视图
		viewName := fmt.Sprintf("popup_%s", popup.ID)
		v, err := g.SetView(viewName, popup.X, popup.Y, popup.X+popup.Width-1, popup.Y+popup.Height-1)
		if err != nil {
			if err != gocui.ErrUnknownView {
				return err
			}
			v.Frame = true
			v.Highlight = true
			v.SelBgColor = gocui.ColorBlue
			
			// 为新创建的弹出窗口绑定鼠标事件
			bindPopupMouseEvents(g, viewName)
			
			// 自动聚焦到新创建的弹出窗口
			g.SetCurrentView(viewName)
		}
		
		// 设置标题
		v.Title = fmt.Sprintf(" %s [可拖动] ", popup.Title)
		
		// 清空并填充内容
		v.Clear()
		
		// 显示关闭按钮提示
		fmt.Fprintf(v, "\x1b[90m按 q 键关闭 | 拖动标题栏移动窗口\x1b[0m\n")
		fmt.Fprintln(v, "")
		
		// 显示内容 (考虑滚动)
		availableLines := popup.Height - 3 // 减去边框和提示行
		if availableLines < 1 {
			availableLines = 1
		}
		
		startIdx := popup.ScrollY
		endIdx := startIdx + availableLines
		if endIdx > len(popup.Content) {
			endIdx = len(popup.Content)
		}
		
		for idx := startIdx; idx < endIdx; idx++ {
			fmt.Fprintln(v, popup.Content[idx])
		}
		
		// 如果有更多内容，显示滚动提示
		if len(popup.Content) > availableLines {
			fmt.Fprintf(v, "\x1b[90m[%d/%d] 使用↑↓滚动\x1b[0m", popup.ScrollY+1, len(popup.Content)-availableLines+1)
		}
		
		// 将窗口移到最顶层 (通过设置TabStop)
		if i == len(ctx.PopupWindows)-1 {
			v.Highlight = true
		}
	}
	
	return nil
}

// ========== 状态栏内容刷新 ==========
func updateStatusView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("status")
	if err != nil {
		return
	}
	
	v.Clear()
	
	// 显示调试器状态
	stateStr := "停止"
	if ctx.BpfLoaded {
		stateStr = "BPF已加载"
	}
	if ctx.Running {
		stateStr = "运行中"
	}
	
	// 显示基本状态信息
	fmt.Fprintf(v, "RISC-V 内核调试器 | 状态: %s | 当前函数: %s | 地址: 0x%X", 
		stateStr, ctx.CurrentFunc, ctx.CurrentAddr)
	
	// 显示全屏状态和操作提示
	if ctx.IsFullscreen {
		fmt.Fprintf(v, " | 🖥️ 全屏模式: %s | F11/ESC-退出全屏", ctx.FullscreenView)
	} else {
		// 显示拖拽状态和提示
		if ctx.Layout != nil {
			if ctx.Layout.IsDragging {
				fmt.Fprintf(v, " | 🔧 正在调整: %s", getBoundaryName(ctx.Layout.DragBoundary))
			} else {
				fmt.Fprint(v, " | 💡 提示: 鼠标拖拽窗口边界调整大小, F11全屏")
			}
			
			// 显示当前布局参数
			fmt.Fprintf(v, " | 布局: L%d R%d C%d", 
				ctx.Layout.LeftPanelWidth, 
				ctx.Layout.RightPanelWidth, 
				ctx.Layout.CommandHeight)
		}
	}
}

// 获取边界名称的友好显示
func getBoundaryName(boundary string) string {
	switch boundary {
	case "left":
		return "左侧边界"
	case "right":
		return "右侧边界"
	case "bottom":
		return "底部边界"
	case "right1":
		return "寄存器/变量分割线"
	case "right2":
		return "变量/堆栈分割线"
	default:
		return "未知边界"
	}
}

// ========== 文件浏览器窗口内容刷新 ==========
func updateFileBrowserView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("filebrowser")
	if err != nil {
		return
	}
	v.Clear()
	
	if g.CurrentView() != nil && g.CurrentView().Name() == "filebrowser" {
		fmt.Fprintln(v, "\x1b[43;30m▶ 文件浏览器 (已聚焦)\x1b[0m")
	} else {
		fmt.Fprintln(v, "文件浏览器")
	}
	
	if ctx.Project == nil {
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "未打开项目")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "使用命令打开项目:")
		fmt.Fprintln(v, "open /path/to/project")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "或者:")
		fmt.Fprintln(v, "open ../tacosys_ko")
		return
	}
	
	fmt.Fprintln(v, "")
	fmt.Fprintf(v, "项目: %s\n", filepath.Base(ctx.Project.RootPath))
	fmt.Fprintln(v, "💡 单击文件打开，单击目录展开/折叠")
	fmt.Fprintln(v, "")
	
	// 显示文件树
	if ctx.Project.FileTree != nil {
		// 重置行映射表
		fileBrowserLineMap = make([]*FileNode, 0)
		fileBrowserDisplayLines = make([]string, 0)
		
		// 显示文件树并构建映射表
		displayFileTreeWithMapping(v, ctx.Project.FileTree, 0, ctx)
	}
}

// 显示文件树
func displayFileTree(v *gocui.View, node *FileNode, depth int, scroll int) {
	if node == nil {
		return
	}
	
	indent := strings.Repeat("  ", depth)
	icon := "📄"
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
		default:
			icon = "📄"
		}
	}
	
	fmt.Fprintf(v, "%s%s %s\n", indent, icon, node.Name)
	
	// 如果是展开的目录，显示子节点
	if node.IsDir && node.Expanded {
		for _, child := range node.Children {
			displayFileTree(v, child, depth+1, scroll)
		}
	}
}

// 新的文件树显示函数，支持行映射和交互
func displayFileTreeWithMapping(v *gocui.View, node *FileNode, depth int, ctx *DebuggerContext) {
	displayFileTreeNode(v, node, depth, ctx)
}

// 递归显示文件树节点并建立映射
func displayFileTreeNode(v *gocui.View, node *FileNode, depth int, ctx *DebuggerContext) {
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
			displayFileTreeNode(v, child, depth+1, ctx)
		}
	}
}

// ========== 寄存器窗口内容刷新 ==========
func updateRegistersView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("registers")
	if err != nil {
		return
	}
	v.Clear()
	if g.CurrentView() != nil && g.CurrentView().Name() == "registers" {
		fmt.Fprintln(v, "\x1b[43;30m▶ 寄存器 (已聚焦)\x1b[0m")
	} else {
		fmt.Fprintln(v, "寄存器")
	}
	lines := []string{
		fmt.Sprintf("PC: 0x%016x", ctx.CurrentAddr),
		fmt.Sprintf("RA: 0x%016x", ctx.CurrentAddr+0x100),
		fmt.Sprintf("SP: 0x%016x", ctx.CurrentAddr+0x200),
		"...",
	}
	for i := regScroll; i < len(lines); i++ {
		fmt.Fprintln(v, lines[i])
	}
}

// ========== 变量窗口内容刷新 ==========
func updateVariablesView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("variables")
	if err != nil {
		return
	}
	v.Clear()
	if g.CurrentView() != nil && g.CurrentView().Name() == "variables" {
		fmt.Fprintln(v, "\x1b[43;30m▶ 变量 (已聚焦)\x1b[0m")
	} else {
		fmt.Fprintln(v, "变量")
	}
	lines := []string{
		"局部变量:",
		"ctx      debugger_ctx_t* 0x7fff1234",
		"fd       int             3",
		"ret      int            -1",
		"...",
		"", "全局变量:",
		"g_ctx    debugger_ctx_t* 0x601020",
		"debug_level int         2",
		"...",
	}
	for i := varScroll; i < len(lines); i++ {
		fmt.Fprintln(v, lines[i])
	}
}

// ========== 调用栈窗口内容刷新 ==========
func updateStackView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("stack")
	if err != nil {
		return
	}
	v.Clear()
	if g.CurrentView() != nil && g.CurrentView().Name() == "stack" {
		fmt.Fprintln(v, "\x1b[43;30m▶ 函数调用堆栈 (已聚焦)\x1b[0m")
	} else {
		fmt.Fprintln(v, "函数调用堆栈")
	}
	lines := []string{
		"#0 taco_sys_init kernel_debugger_tui.c:156",
		"#1 taco_sys_mmz_alloc taco_sys_mmz.c:89",
		"#2 taco_sys_init taco_sys_init.c:45",
		"...",
	}
	for i := stackScroll; i < len(lines); i++ {
		fmt.Fprintln(v, lines[i])
	}
}

// ========== 代码窗口内容刷新 ==========
func updateCodeView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("code")
	if err != nil {
		return
	}
	v.Clear()
	
	if g.CurrentView() != nil && g.CurrentView().Name() == "code" {
		fmt.Fprintln(v, "\x1b[43;30m▶ 代码视图 (已聚焦)\x1b[0m")
	} else {
		fmt.Fprintln(v, "代码视图")
	}
	
	// 如果有打开的文件，显示文件内容
	if ctx.Project != nil && ctx.Project.CurrentFile != "" {
		lines, exists := ctx.Project.OpenFiles[ctx.Project.CurrentFile]
		if !exists {
			// 尝试读取文件
			var err error
			lines, err = readFileContent(ctx.Project.CurrentFile)
			if err != nil {
				fmt.Fprintf(v, "无法读取文件: %v\n", err)
				return
			}
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
			
			// 显示行号和断点标记
			if hasBreakpoint {
				fmt.Fprintf(v, "%3d● %s\n", lineNum, line)
			} else {
				fmt.Fprintf(v, "%3d: %s\n", lineNum, line)
			}
		}
		
	} else {
		// 默认显示汇编代码
		fmt.Fprintln(v, "汇编代码 (示例)")
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

// ========== 断点窗口内容刷新 ==========
func updateBreakpointsView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("stack")
	if err != nil {
		return
	}
	v.Clear()
	
	if g.CurrentView() != nil && g.CurrentView().Name() == "stack" {
		fmt.Fprintln(v, "\x1b[43;30m▶ 断点管理 (已聚焦)\x1b[0m")
	} else {
		fmt.Fprintln(v, "断点管理")
	}
	
	if ctx.Project == nil {
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "未打开项目")
		return
	}
	
	fmt.Fprintln(v, "")
	if len(ctx.Project.Breakpoints) == 0 {
		fmt.Fprintln(v, "无断点")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "在代码视图中按Enter设置断点")
	} else {
		fmt.Fprintf(v, "断点列表 (%d个):\n", len(ctx.Project.Breakpoints))
		fmt.Fprintln(v, "")
		
		for i, bp := range ctx.Project.Breakpoints {
			status := "✓"
			if !bp.Enabled {
				status = "✗"
			}
			
			fileName := filepath.Base(bp.File)
			fmt.Fprintf(v, "%d. %s %s:%d\n", i+1, status, fileName, bp.Line)
			if bp.Function != "unknown" {
				fmt.Fprintf(v, "   函数: %s\n", bp.Function)
			}
		}
		
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "g-生成BPF  c-清除所有断点")
	}
}

// ========== 命令窗口内容刷新 ==========
func updateCommandView(g *gocui.Gui, ctx *DebuggerContext) {
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
							debugInfo := fmt.Sprintf("[DEBUG] 粘贴检测: 长度=%d, 内容=%s", len(actualInput), actualInput)
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
		
		fmt.Fprintln(v, "命令终端 - 按F6聚焦")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "基本命令:")
		fmt.Fprintln(v, "  help         - 显示帮助")
		fmt.Fprintln(v, "  open <路径>  - 打开项目")
		fmt.Fprintln(v, "  clear        - 清屏")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "快捷键: Tab-切换窗口")
		
		// 显示项目状态
		if ctx.Project != nil {
			fmt.Fprintln(v, "")
			fmt.Fprintf(v, "项目: %s", filepath.Base(ctx.Project.RootPath))
		}
		
		// 显示最近的几条命令历史（如果有的话）
		if len(ctx.CommandHistory) > 0 {
			fmt.Fprintln(v, "")
			fmt.Fprintln(v, "最近命令:")
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

// ========== 刷新所有窗口 ==========
func updateAllViews(g *gocui.Gui, ctx *DebuggerContext) {
	updateStatusView(g, ctx)
	updateFileBrowserView(g, ctx)
	updateRegistersView(g, ctx)
	updateVariablesView(g, ctx)
	updateBreakpointsView(g, ctx)
	updateCodeView(g, ctx)
	updateCommandView(g, ctx)
}

// ========== 文本选择功能 ==========

// 复制选中文本到系统剪贴板
func copyToClipboard(text string) error {
	// 方法1: 尝试使用OSC52 (适用于SSH和现代终端)
	if err := copyWithOSC52(text); err == nil {
		return nil
	}
	
	// 方法2: 尝试xclip
	if err := exec.Command("xclip", "-selection", "clipboard").Run(); err == nil {
		cmd := exec.Command("xclip", "-selection", "clipboard")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	
	// 方法3: 尝试xsel
	if err := exec.Command("xsel", "--version").Run(); err == nil {
		cmd := exec.Command("xsel", "--clipboard", "--input")
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return nil
		}
	}
	
	return fmt.Errorf("无法访问剪贴板，请安装xclip或xsel")
}

func copyWithOSC52(text string) error {
	// 简化的OSC52实现 - 需要base64编码
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	osc52Sequence := fmt.Sprintf("\033]52;c;%s\007", encoded)
	_, err := os.Stderr.Write([]byte(osc52Sequence))
	return err
}

// 获取当前窗口的文本内容
func getViewText(g *gocui.Gui, viewName string) []string {
	v, err := g.View(viewName)
	if err != nil {
		return nil
	}
	
	// 获取视图的缓冲区内容
	buffer := v.Buffer()
	lines := strings.Split(strings.TrimSuffix(buffer, "\n"), "\n")
	return lines
}

// 处理Enter键选择当前行
func selectCurrentLine(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	
	// 获取当前光标位置
	_, cy := v.Cursor()
	lines := getViewText(g, v.Name())
	
	if cy < len(lines) && cy >= 0 {
		selectedText := strings.TrimSpace(lines[cy])
		if selectedText != "" {
			// 复制到剪贴板
			copyToClipboard(selectedText)
			
			// 显示选择结果
			if globalCtx != nil {
				globalCtx.SelectionMode = true
				globalCtx.SelectionView = v.Name()
				globalCtx.SelectionText = selectedText
			}
		}
	}
	return nil
}

// 处理双击选择单词
func selectWordAtCursor(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	
	cx, cy := v.Cursor()
	lines := getViewText(g, v.Name())
	
	if cy < len(lines) && cy >= 0 {
		line := lines[cy]
		if cx < len(line) {
			// 找到单词边界
			start := cx
			end := cx
			
			// 向左找单词开始
			for start > 0 && isWordChar(line[start-1]) {
				start--
			}
			
			// 向右找单词结束
			for end < len(line) && isWordChar(line[end]) {
				end++
			}
			
			if start < end {
				selectedText := line[start:end]
				copyToClipboard(selectedText)
				
				if globalCtx != nil {
					globalCtx.SelectionMode = true
					globalCtx.SelectionView = v.Name()
					globalCtx.SelectionText = selectedText
				}
			}
		}
	}
	return nil
}

// 判断是否为单词字符
func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || 
	       (c >= '0' && c <= '9') || c == '_' || c == 'x'
}

// 清除选择状态
func clearSelection(g *gocui.Gui, v *gocui.View) error {
	if globalCtx != nil {
		globalCtx.SelectionMode = false
		globalCtx.SelectionView = ""
		globalCtx.SelectionText = ""
	}
	return nil
}

// ========== 鼠标事件处理（gocui v0.5.0 兼容实现） ==========
func mouseFocusHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	g.SetCurrentView(v.Name())
	return nil
}

func mouseScrollUpHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	scrollWindowByName(v.Name(), -1)
	return nil
}

func mouseScrollDownHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	scrollWindowByName(v.Name(), 1)
	return nil
}

// ========== 键盘滚动处理 ==========
func scrollUpHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	scrollWindowByName(v.Name(), -1)
	return nil
}

func scrollDownHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	scrollWindowByName(v.Name(), 1)
	return nil
}

// ========== 窗口切换处理 ==========
func nextViewHandler(g *gocui.Gui, v *gocui.View) error {
	views := []string{"filebrowser", "registers", "variables", "stack", "code", "command"}
	currentView := g.CurrentView()
	if currentView == nil {
		g.SetCurrentView("filebrowser")
		return nil
	}
	
	currentName := currentView.Name()
	for i, name := range views {
		if name == currentName {
			nextIndex := (i + 1) % len(views)
			g.SetCurrentView(views[nextIndex])
			break
		}
	}
	return nil
}

func prevViewHandler(g *gocui.Gui, v *gocui.View) error {
	views := []string{"filebrowser", "registers", "variables", "stack", "code", "command"}
	currentView := g.CurrentView()
	if currentView == nil {
		g.SetCurrentView("filebrowser")
		return nil
	}
	
	currentName := currentView.Name()
	for i, name := range views {
		if name == currentName {
			prevIndex := (i - 1 + len(views)) % len(views)
			g.SetCurrentView(views[prevIndex])
			break
		}
	}
	return nil
}

// ========== 直接窗口切换 ==========
func switchToFileBrowser(g *gocui.Gui, v *gocui.View) error {
	g.SetCurrentView("filebrowser")
	return nil
}

func switchToRegisters(g *gocui.Gui, v *gocui.View) error {
	g.SetCurrentView("registers")
	return nil
}

func switchToVariables(g *gocui.Gui, v *gocui.View) error {
	g.SetCurrentView("variables")
	return nil
}

func switchToStack(g *gocui.Gui, v *gocui.View) error {
	g.SetCurrentView("stack")
	return nil
}

func switchToCode(g *gocui.Gui, v *gocui.View) error {
	g.SetCurrentView("code")
	return nil
}

func switchToCommand(g *gocui.Gui, v *gocui.View) error {
	g.SetCurrentView("command")
	// 标记命令窗口需要重绘（获得焦点时）
	if globalCtx != nil {
		globalCtx.CommandDirty = true
	}
	return nil
}

func scrollWindowByName(name string, direction int) {
	switch name {
	case "filebrowser":
		fileScroll += direction
		if fileScroll < 0 {
			fileScroll = 0
		}
	case "registers":
		regScroll += direction
		if regScroll < 0 {
			regScroll = 0
		}
	case "variables":
		varScroll += direction
		if varScroll < 0 {
			varScroll = 0
		}
	case "stack":
		stackScroll += direction
		if stackScroll < 0 {
			stackScroll = 0
		}
	case "code":
		codeScroll += direction
		if codeScroll < 0 {
			codeScroll = 0
		}
	}
}

// ========== 事件处理函数 ==========

// 处理文件选择（旧的键盘版本，保留向后兼容）
func handleFileSelection(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Project == nil {
		return nil
	}
	
	// 简化实现：选择第一个C文件
	if globalCtx.Project.FileTree != nil {
		for _, child := range globalCtx.Project.FileTree.Children {
			if !child.IsDir && strings.HasSuffix(child.Name, ".c") {
				globalCtx.Project.CurrentFile = child.Path
				codeScroll = 0 // 重置滚动位置
				break
			}
		}
	}
	
	return nil
}

// 处理文件浏览器鼠标点击
func handleFileBrowserClick(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Project == nil {
		// 即使没有项目，也要确保聚焦到文件浏览器
		g.SetCurrentView("filebrowser")
		return nil
	}
	
	// 首先聚焦到文件浏览器
	g.SetCurrentView("filebrowser")
	
	// 获取鼠标点击位置
	_, cy := v.Cursor()
	
	// 计算实际点击的行号（考虑标题行和滚动偏移）
	// 文件浏览器有5行标题：标题行、空行、项目名、提示行、空行
	headerLines := 5
	clickedLine := cy - headerLines + fileScroll
	
	// 检查点击行是否有效
	if clickedLine < 0 || clickedLine >= len(fileBrowserLineMap) {
		return nil
	}
	
	// 获取对应的文件节点
	node := fileBrowserLineMap[clickedLine]
	if node == nil {
		return nil
	}
	
	if node.IsDir {
		// 点击目录：切换展开/折叠状态
		node.Expanded = !node.Expanded
		
		// 更新文件浏览器显示
		g.Update(func(g *gocui.Gui) error {
			updateFileBrowserView(g, globalCtx)
			return nil
		})
		
		// 保持在文件浏览器聚焦状态
		
	} else {
		// 点击文件：在代码视图中打开
		globalCtx.Project.CurrentFile = node.Path
		codeScroll = 0 // 重置代码视图滚动位置
		
		// 更新所有视图以反映文件打开状态
		g.Update(func(g *gocui.Gui) error {
			updateAllViews(g, globalCtx)
			return nil
		})
		
		// 自动切换到代码视图
		g.SetCurrentView("code")
	}
	
	return nil
}

// 处理断点设置
func handleBreakpointToggle(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Project == nil || globalCtx.Project.CurrentFile == "" {
		return nil
	}
	
	// 获取当前行号（简化实现）
	_, cy := v.Cursor()
	lineNum := codeScroll + cy + 1 // 考虑滚动偏移
	
	// 切换断点
	addBreakpoint(globalCtx, globalCtx.Project.CurrentFile, lineNum)
	
	return nil
}

// 处理代码视图鼠标点击（支持双击设置断点）
func handleCodeViewClick(g *gocui.Gui, v *gocui.View) error {
	// 首先聚焦到代码视图
	g.SetCurrentView("code")
	
	if globalCtx == nil || globalCtx.Project == nil || globalCtx.Project.CurrentFile == "" {
		// 如果没有打开文件，只需要聚焦即可
		return nil
	}
	
	// 获取点击位置
	_, cy := v.Cursor()
	currentTime := time.Now()
	
	// 计算实际点击的代码行号（考虑标题行和滚动偏移）
	// 代码视图有2行标题：标题行、文件名行
	headerLines := 2
	clickedCodeLine := cy - headerLines + codeScroll
	
	// 检查是否是有效的代码行
	if clickedCodeLine < 0 {
		return nil
	}
	
	// 计算实际的源代码行号（从1开始）
	sourceLineNum := clickedCodeLine + 1
	
	// 检查是否是双击（300毫秒内在同一行点击两次）
	isDoubleClick := false
	if globalCtx.LastClickLine == sourceLineNum && 
	   currentTime.Sub(globalCtx.LastClickTime) < 300*time.Millisecond {
		isDoubleClick = true
	}
	
	// 更新点击状态
	globalCtx.LastClickTime = currentTime
	globalCtx.LastClickLine = sourceLineNum
	
	if isDoubleClick {
		// 双击：设置/取消断点
		lines, exists := globalCtx.Project.OpenFiles[globalCtx.Project.CurrentFile]
		if !exists {
			var err error
			lines, err = readFileContent(globalCtx.Project.CurrentFile)
			if err != nil {
				return nil
			}
			globalCtx.Project.OpenFiles[globalCtx.Project.CurrentFile] = lines
		}
		
		// 检查行号是否有效
		if sourceLineNum <= len(lines) {
			addBreakpoint(globalCtx, globalCtx.Project.CurrentFile, sourceLineNum)
			
			// 更新所有视图以反映断点变化
			g.Update(func(g *gocui.Gui) error {
				updateAllViews(g, globalCtx)
				return nil
			})
		}
	}
	
	return nil
}

// 处理命令输入
func handleCommand(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil {
		return nil
	}
	
	// 获取当前输入的命令
	command := strings.TrimSpace(globalCtx.CurrentInput)
	
	// 如果命令为空，只是换行
	if command == "" {
		// 添加空行到历史记录
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, ">")
		globalCtx.CurrentInput = ""
		// 标记需要重绘
		globalCtx.CommandDirty = true
		return nil
	}
	
	// 调试信息：记录截断检测
	if len(command) > 40 && strings.Contains(command, "linux-6.") {
		debugInfo := fmt.Sprintf("[DEBUG] 路径命令长度=%d: %s", len(command), command)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugInfo)
	}
	
	// 将命令添加到历史记录
	globalCtx.CommandHistory = append(globalCtx.CommandHistory, fmt.Sprintf("> %s", command))
	
	// 智能解析命令 - 保留空格
	var cmd, args string
	spaceIndex := strings.Index(command, " ")
	if spaceIndex == -1 {
		cmd = command
		args = ""
	} else {
		cmd = command[:spaceIndex]
		args = strings.TrimSpace(command[spaceIndex+1:])
	}
	
	// 执行命令并获取输出
	var output []string
	
	switch cmd {
	case "help", "h":
		output = []string{
			"可用命令:",
			"  help         - 显示此帮助信息",
			"  clear        - 清屏",
			"  open <路径>  - 打开项目目录（支持带空格的路径）",
			"  bp           - 查看所有断点（弹出窗口）",
			"  bp clear     - 清除所有断点",
			"  breakpoints  - 查看所有断点（同bp）",
			"  breakpoint   - 清除所有断点（同bp clear）",
			"  generate     - 生成BPF调试代码",
			"  close        - 关闭当前项目",
			"  pwd          - 显示当前工作目录",
			"",
			"断点功能:",
			"  • 双击代码行设置/切换断点",
			"  • Enter键也可设置断点",
			"  • 断点自动保存到.debug_breakpoints.json",
			"  • 重新打开项目时自动加载断点",
			"",
			"导航快捷键:",
			"  Tab - 切换窗口",
			"  F1-F6 - 直接切换到指定窗口",
			"  F11 - 全屏切换",
			"  ESC - 退出全屏/关闭弹出窗口",
		}
		
	case "clear":
		// 清屏 - 清空命令历史
		globalCtx.CommandHistory = []string{}
		globalCtx.CurrentInput = ""
		// 标记需要重绘
		globalCtx.CommandDirty = true
		return nil
		
	case "pwd":
		wd, _ := os.Getwd()
		output = []string{wd}
		
	case "open":
		if args == "" {
			output = []string{"错误: 用法: open <项目路径>", "提示: 支持带空格的路径，如: open /path/to/folder with spaces"}
		} else {
			projectPath := args  // 直接使用args，保留所有空格
			output = append(output, fmt.Sprintf("正在处理路径: %s", projectPath))
			
			// 如果是相对路径，转换为绝对路径
			if !filepath.IsAbs(projectPath) {
				wd, _ := os.Getwd()
				projectPath = filepath.Join(wd, projectPath)
				output = append(output, fmt.Sprintf("转换为绝对路径: %s", projectPath))
			}
			
			// 检查路径是否存在
			if _, err := os.Stat(projectPath); os.IsNotExist(err) {
				output = []string{fmt.Sprintf("错误: 路径不存在: %s", projectPath)}
			} else {
				output = append(output, "路径存在，开始打开项目...")
				
				project, err := openProject(projectPath)
				if err != nil {
					output = append(output, fmt.Sprintf("错误: 打开项目失败: %v", err))
				} else {
					globalCtx.Project = project
					fileCount := countFiles(project.FileTree)
					output = append(output, []string{
						fmt.Sprintf("成功打开项目: %s", filepath.Base(projectPath)),
						fmt.Sprintf("找到 %d 个文件", fileCount),
						"使用F1切换到文件浏览器查看文件树",
					}...)
				}
			}
		}
		
	case "generate", "g":
		if globalCtx.Project == nil {
			output = []string{"错误: 请先打开项目"}
		} else {
			err := generateBPF(globalCtx)
			if err != nil {
				output = []string{fmt.Sprintf("错误: 生成BPF失败: %v", err)}
			} else {
				output = []string{
					"成功: BPF代码生成完成",
					"文件: debug_breakpoints.bpf.c",
				}
				globalCtx.BpfLoaded = true
			}
		}
		
	case "breakpoint":
		if globalCtx.Project != nil {
			count := len(globalCtx.Project.Breakpoints)
			globalCtx.Project.Breakpoints = make([]Breakpoint, 0)
			// 保存清空后的断点列表
			if err := saveBreakpoints(globalCtx); err != nil {
				output = []string{fmt.Sprintf("警告: 清除断点成功但保存失败: %v", err)}
			} else {
				output = []string{fmt.Sprintf("成功: 已清除 %d 个断点", count)}
			}
		} else {
			output = []string{"提示: 没有打开的项目"}
		}
		
	case "bp":
		if args == "clear" {
			// bp clear - 清除所有断点
			if globalCtx.Project != nil {
				count := len(globalCtx.Project.Breakpoints)
				globalCtx.Project.Breakpoints = make([]Breakpoint, 0)
				// 保存清空后的断点列表
				if err := saveBreakpoints(globalCtx); err != nil {
					output = []string{fmt.Sprintf("警告: 清除断点成功但保存失败: %v", err)}
				} else {
					output = []string{fmt.Sprintf("成功: 已清除 %d 个断点", count)}
				}
			} else {
				output = []string{"提示: 没有打开的项目"}
			}
		} else {
			// bp - 查看断点（默认行为）
			if globalCtx.Project == nil {
				output = []string{"错误: 请先打开项目"}
			} else {
				// 创建断点查看弹出窗口
				showBreakpointsPopup(globalCtx)
				output = []string{"断点查看窗口已打开"}
			}
		}
		
	case "close":
		if globalCtx.Project != nil {
			projectName := filepath.Base(globalCtx.Project.RootPath)
			globalCtx.Project = nil
			output = []string{fmt.Sprintf("成功: 已关闭项目 %s", projectName)}
		} else {
			output = []string{"提示: 没有打开的项目"}
		}
		
	case "breakpoints":
		if globalCtx.Project == nil {
			output = []string{"错误: 请先打开项目"}
		} else {
			// 创建断点查看弹出窗口
			showBreakpointsPopup(globalCtx)
			output = []string{"断点查看窗口已打开"}
		}
		
	case "status":
		output = []string{
			fmt.Sprintf("调试器状态: %s", globalCtx.CurrentFunc),
			fmt.Sprintf("当前地址: 0x%X", globalCtx.CurrentAddr),
		}
		if globalCtx.Project != nil {
			output = append(output, fmt.Sprintf("项目: %s", filepath.Base(globalCtx.Project.RootPath)))
			output = append(output, fmt.Sprintf("断点数: %d", len(globalCtx.Project.Breakpoints)))
		} else {
			output = append(output, "项目: 未打开")
		}
		
	default:
		output = []string{
			fmt.Sprintf("bash: %s: command not found", cmd),
			"输入 'help' 查看可用命令",
		}
	}
	
	// 将输出添加到历史记录
	for _, line := range output {
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, line)
	}
	
	// 清空当前输入，准备下一条命令
	globalCtx.CurrentInput = ""
	// 标记需要重绘
	globalCtx.CommandDirty = true
	
	return nil
}

// 辅助函数：计算文件树中的文件数量
func countFiles(node *FileNode) int {
	if node == nil {
		return 0
	}
	
	count := 0
	if !node.IsDir {
		count = 1
	}
	
	for _, child := range node.Children {
		count += countFiles(child)
	}
	
	return count
}

// 显示断点查看弹出窗口
func showBreakpointsPopup(ctx *DebuggerContext) {
	if ctx == nil || ctx.Project == nil {
		return
	}
	
	var content []string
	
	if len(ctx.Project.Breakpoints) == 0 {
		content = []string{
			"当前没有设置断点",
			"",
			"使用方法:",
			"• 在代码视图中双击代码行设置断点",
			"• 按Enter键也可以设置断点",
			"• 再次点击相同行可切换断点启用/禁用状态",
		}
	} else {
		content = append(content, fmt.Sprintf("共有 %d 个断点:", len(ctx.Project.Breakpoints)))
		content = append(content, "")
		content = append(content, "状态 | 文件 | 行号 | 函数")
		content = append(content, "---- | ---- | ---- | ----")
		
		for i, bp := range ctx.Project.Breakpoints {
			status := "✓ 启用"
			if !bp.Enabled {
				status = "✗ 禁用"
			}
			
			fileName := filepath.Base(bp.File)
			function := bp.Function
			if function == "unknown" {
				function = "-"
			}
			
			line := fmt.Sprintf("%2d.  %s | %s | %d | %s", 
				i+1, status, fileName, bp.Line, function)
			content = append(content, line)
		}
		
		content = append(content, "")
		content = append(content, "操作说明:")
		content = append(content, "• 断点会自动保存到 .debug_breakpoints.json")
		content = append(content, "• 重新打开项目时会自动加载断点")
		content = append(content, "• 使用命令 'generate' 生成BPF调试代码")
		content = append(content, "")
		content = append(content, "🔥 关闭窗口: 按 q 键 或 点击任意窗口边界外区域")
	}
	
	// 计算合适的窗口大小
	width := 60
	height := len(content) + 5 // 内容 + 边框 + 提示行
	if height > 20 {
		height = 20 // 最大高度
	}
	if height < 8 {
		height = 8 // 最小高度
	}
	
	// 创建弹出窗口
	popup := createPopupWindow(ctx, "breakpoints", "断点查看器", width, height, content)
	showPopupWindow(ctx, popup)
}

// 处理字符输入
func handleCharInput(ch rune) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if globalCtx == nil {
			return nil
		}
		
		// 调试信息：仅记录关键问题
		if ch == '.' && len(globalCtx.CommandHistory) < 10 {
			currentViewName := "none"
			if g.CurrentView() != nil {
				currentViewName = g.CurrentView().Name()
			}
			debugInfo := fmt.Sprintf("[DEBUG] 点号输入，视图: %s, 当前输入长度: %d", currentViewName, len(globalCtx.CurrentInput))
			globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugInfo)
			globalCtx.CommandDirty = true
		}
		
		// 只在命令窗口聚焦时处理字符输入
		if g.CurrentView() != nil && g.CurrentView().Name() == "command" {
			// 将字符添加到当前输入
			globalCtx.CurrentInput += string(ch)
			// 标记需要重绘
			globalCtx.CommandDirty = true
		}
		
		return nil
	}
}

// 处理退格键
func handleBackspace(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil {
		return nil
	}
	
	// 只在命令窗口聚焦时处理退格
	if g.CurrentView() != nil && g.CurrentView().Name() == "command" {
		// 删除当前输入的最后一个字符
		if len(globalCtx.CurrentInput) > 0 {
			globalCtx.CurrentInput = globalCtx.CurrentInput[:len(globalCtx.CurrentInput)-1]
			// 标记需要重绘
			globalCtx.CommandDirty = true
		}
	}
	
	return nil
}

// 清空当前输入
func clearCurrentInput(g *gocui.Gui, v *gocui.View) error {
	if globalCtx != nil {
		globalCtx.CurrentInput = ""
		// 标记需要重绘
		globalCtx.CommandDirty = true
	}
	return nil
}

// 生成BPF快捷键
func generateBPFHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Project == nil {
		return nil
	}
	
	err := generateBPF(globalCtx)
	if err != nil {
		// 在命令窗口显示错误
		if cmdView, err := g.View("command"); err == nil {
			cmdView.Clear()
			fmt.Fprintf(cmdView, "生成BPF失败: %v\n", err)
		}
	} else {
		globalCtx.BpfLoaded = true
		// 在命令窗口显示成功
		if cmdView, err := g.View("command"); err == nil {
			cmdView.Clear()
			fmt.Fprintln(cmdView, "BPF代码生成成功!")
		}
	}
	
	return nil
}

// 清除断点快捷键
func clearBreakpointsHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx != nil && globalCtx.Project != nil {
		globalCtx.Project.Breakpoints = make([]Breakpoint, 0)
		
		// 在命令窗口显示消息
		if cmdView, err := g.View("command"); err == nil {
			cmdView.Clear()
			fmt.Fprintln(cmdView, "已清除所有断点")
		}
	}
	
	return nil
}

// 鼠标按下开始选择
func mouseSelectStartHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil || globalCtx == nil {
		return nil
	}
	
	// 获取全局context
	ctx := globalCtx
	
	// 获取鼠标位置（原版gocui没有MousePosition方法，简化处理）
	ox, oy := v.Origin()
	cx, cy := v.Cursor()
	
	// 简化：使用光标位置作为选择起点
	if true {
		ctx.MouseSelecting = true
		ctx.SelectStartX = ox + cx
		ctx.SelectStartY = oy + cy
		ctx.SelectEndX = ctx.SelectStartX
		ctx.SelectEndY = ctx.SelectStartY
		ctx.SelectionView = v.Name()
	}
	
	return nil
}

// 鼠标拖拽选择
func mouseDragHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil || globalCtx == nil {
		return nil
	}
	
	ctx := globalCtx
	if !ctx.MouseSelecting || ctx.SelectionView != v.Name() {
		return nil
	}
	
	// 获取当前光标位置（简化处理）
	ox, oy := v.Origin()
	cx, cy := v.Cursor()
	
	// 简化：使用光标位置更新选择终点
	if true {
		ctx.SelectEndX = ox + cx
		ctx.SelectEndY = oy + cy
		
		// 高亮选中区域（简单实现）
		g.Update(func(g *gocui.Gui) error {
			return nil
		})
	}
	
	return nil
}

// 鼠标释放完成选择
func mouseSelectEndHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil || globalCtx == nil {
		return nil
	}
	
	ctx := globalCtx
	if !ctx.MouseSelecting || ctx.SelectionView != v.Name() {
		return nil
	}
	
	// 获取选中的文本
	selectedText := getSelectedText(g, v, ctx)
	if selectedText != "" {
		ctx.SelectionText = selectedText
		ctx.SelectionMode = true
		
		// 自动复制到剪贴板
		if err := copyToClipboard(selectedText); err != nil {
			// 在命令窗口显示错误
			if cmdView, err := g.View("command"); err == nil {
				fmt.Fprintf(cmdView, "\n复制失败: %v", err)
			}
		} else {
			// 在命令窗口显示成功信息
			if cmdView, err := g.View("command"); err == nil {
				fmt.Fprintf(cmdView, "\n已复制选中文本: %.30s...", selectedText)
			}
		}
	}
	
	ctx.MouseSelecting = false
	return nil
}

// 获取选中的文本
func getSelectedText(g *gocui.Gui, v *gocui.View, ctx *DebuggerContext) string {
	if ctx.SelectStartY == ctx.SelectEndY {
		// 同一行选择
		return getTextFromLine(v, ctx.SelectStartY, ctx.SelectStartX, ctx.SelectEndX)
	} else {
		// 多行选择
		var result strings.Builder
		startY := ctx.SelectStartY
		endY := ctx.SelectEndY
		if startY > endY {
			startY, endY = endY, startY
		}
		
		for line := startY; line <= endY; line++ {
			if line == startY {
				// 第一行：从开始位置到行尾
				result.WriteString(getTextFromLine(v, line, ctx.SelectStartX, -1))
			} else if line == endY {
				// 最后一行：从行首到结束位置
				result.WriteString(getTextFromLine(v, line, 0, ctx.SelectEndX))
			} else {
				// 中间行：整行
				result.WriteString(getTextFromLine(v, line, 0, -1))
			}
			if line < endY {
				result.WriteString("\n")
			}
		}
		return result.String()
	}
}

// 从视图的指定行获取文本
func getTextFromLine(v *gocui.View, lineNum, startX, endX int) string {
	lines := strings.Split(v.ViewBuffer(), "\n")
	if lineNum < 0 || lineNum >= len(lines) {
		return ""
	}
	
	line := lines[lineNum]
	if startX < 0 {
		startX = 0
	}
	if endX < 0 || endX > len(line) {
		endX = len(line)
	}
	if startX > endX {
		startX, endX = endX, startX
	}
	
	if startX >= len(line) {
		return ""
	}
	
	return line[startX:endX]
}

// ========== 拖拽事件处理 ==========

// 鼠标按下处理 - 检测是否开始拖拽
func mouseDownHandler(g *gocui.Gui, v *gocui.View) error {
	// 首先聚焦到被点击的窗口
	if v != nil {
		g.SetCurrentView(v.Name())
	}
	
	if globalCtx == nil {
		return nil
	}
	
	// 获取鼠标位置（简化实现，使用视图相对位置）
	maxX, maxY := g.Size()
	
	// 这里需要获取实际的鼠标坐标，但gocui原版没有直接的API
	// 我们通过检测当前视图和光标位置来模拟
	if v != nil {
		ox, oy := v.Origin()
		cx, cy := v.Cursor()
		mouseX := ox + cx
		mouseY := oy + cy
		
		// 首先检查是否点击了弹出窗口
		popup := getPopupWindowAt(globalCtx, mouseX, mouseY)
		if popup != nil {
			// 检查是否点击了标题栏（用于拖拽）
			if isInPopupTitleBar(popup, mouseX, mouseY) {
				// 开始拖拽弹出窗口
				popup.Dragging = true
				popup.DragStartX = mouseX - popup.X
				popup.DragStartY = mouseY - popup.Y
				globalCtx.DraggingPopup = popup
				
				// 将此窗口移到最前面
				for i, p := range globalCtx.PopupWindows {
					if p.ID == popup.ID {
						// 移除当前位置的窗口
						globalCtx.PopupWindows = append(globalCtx.PopupWindows[:i], globalCtx.PopupWindows[i+1:]...)
						// 添加到末尾（最前面）
						globalCtx.PopupWindows = append(globalCtx.PopupWindows, popup)
						break
					}
				}
				return nil
			}
			// 如果点击了弹出窗口但不是标题栏，不做处理，让弹出窗口获得焦点
			return nil
		} else if len(globalCtx.PopupWindows) > 0 {
			// 如果有弹出窗口但没有点击到任何一个，说明点击了窗口外部区域
			// 关闭最顶层的弹出窗口
			if len(globalCtx.PopupWindows) > 0 {
				lastPopup := globalCtx.PopupWindows[len(globalCtx.PopupWindows)-1]
				if err := closePopupWindowWithView(g, globalCtx, lastPopup.ID); err == nil {
					debugMsg := fmt.Sprintf("[DEBUG] 点击外部区域关闭弹出窗口: %s", lastPopup.ID)
					globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
					globalCtx.CommandDirty = true
				}
				return nil
			}
		}
		
		// 如果没有点击弹出窗口，检查是否在可拖拽边界上（布局调整）
		if globalCtx.Layout != nil {
			boundary := detectResizeBoundary(mouseX, mouseY, globalCtx.Layout, maxX, maxY)
			if boundary != "" {
				startDrag(boundary, mouseX, mouseY, globalCtx.Layout)
				return nil
			}
		}
	}
	
	return nil
}

// 处理命令窗口鼠标点击
func handleCommandClick(g *gocui.Gui, v *gocui.View) error {
	// 聚焦到命令窗口
	g.SetCurrentView("command")
	
	// 标记命令窗口需要重绘（获得焦点时）
	if globalCtx != nil {
		globalCtx.CommandDirty = true
	}
	
	return nil
}

// 鼠标拖拽处理
func mouseDragResizeHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil {
		return nil
	}
	
	maxX, maxY := g.Size()
	
	// 获取当前鼠标位置（简化实现）
	if v != nil {
		ox, oy := v.Origin()
		cx, cy := v.Cursor()
		mouseX := ox + cx
		mouseY := oy + cy
		
		// 首先检查是否在拖拽弹出窗口
		if globalCtx.DraggingPopup != nil && globalCtx.DraggingPopup.Dragging {
			// 计算新位置
			newX := mouseX - globalCtx.DraggingPopup.DragStartX
			newY := mouseY - globalCtx.DraggingPopup.DragStartY
			
			// 边界检查
			if newX < 0 { newX = 0 }
			if newY < 0 { newY = 0 }
			if newX + globalCtx.DraggingPopup.Width > maxX {
				newX = maxX - globalCtx.DraggingPopup.Width
			}
			if newY + globalCtx.DraggingPopup.Height > maxY {
				newY = maxY - globalCtx.DraggingPopup.Height
			}
			
			// 更新窗口位置
			globalCtx.DraggingPopup.X = newX
			globalCtx.DraggingPopup.Y = newY
			
			return nil
		}
		
		// 如果没有在拖拽弹出窗口，检查布局拖拽
		if globalCtx.Layout != nil && globalCtx.Layout.IsDragging {
			// 处理拖拽移动
			handleDragMove(mouseX, mouseY, globalCtx.Layout, maxX, maxY)
		}
	}
	
	return nil
}

// 鼠标释放处理 - 结束拖拽
func mouseUpHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx != nil {
		// 结束弹出窗口拖拽
		if globalCtx.DraggingPopup != nil && globalCtx.DraggingPopup.Dragging {
			globalCtx.DraggingPopup.Dragging = false
			globalCtx.DraggingPopup = nil
		}
		
		// 结束布局拖拽
		if globalCtx.Layout != nil && globalCtx.Layout.IsDragging {
			endDrag(globalCtx.Layout)
		}
	}
	return nil
}

func main() {
	// 创建调试器上下文
	ctx := &DebuggerContext{
		State:          DEBUG_STOPPED,
		CurrentFocus:   0,
		BpfLoaded:      false,
		CurrentFunc:    "main",
		CurrentAddr:    0x400000,
		Running:        false,
		MouseEnabled:   false,
		CommandHistory: make([]string, 0),  // 初始化命令历史
		CurrentInput:   "",                 // 初始化当前输入
		CommandDirty:   true,               // 初始时需要重绘
		LastClickTime:  time.Time{},        // 初始化双击检测时间
		LastClickLine:  0,                  // 初始化双击检测行号
		IsFullscreen:   false,              // 初始化全屏状态
		FullscreenView: "",                 // 初始化全屏视图
		SavedLayout:    nil,                // 初始化保存的布局
		PopupWindows:   make([]*PopupWindow, 0), // 初始化弹出窗口列表
		DraggingPopup:  nil,                // 初始化拖拽状态
	}
	
	// 设置全局上下文
	globalCtx = ctx

	// 创建GUI
	g, err := gocui.NewGui(gocui.OutputNormal)
	if err != nil {
		log.Panicln(err)
	}
	defer g.Close()

	// 启用鼠标支持
	g.Mouse = true
	ctx.MouseEnabled = true

	g.SetManagerFunc(layout)

	// 设置全局键绑定
	if err := g.SetKeybinding("", gocui.KeyCtrlC, gocui.ModNone, quit); err != nil {
		log.Panicln(err)
	}

	// Tab键循环切换窗口
	if err := g.SetKeybinding("", gocui.KeyTab, gocui.ModNone, nextViewHandler); err != nil {
		log.Panicln(err)
	}

	// 反引号键反向切换窗口
	if err := g.SetKeybinding("", '`', gocui.ModNone, prevViewHandler); err != nil {
		log.Panicln(err)
	}

	// F1-F6功能键直接切换窗口（避免与命令输入冲突）
	if err := g.SetKeybinding("", gocui.KeyF1, gocui.ModNone, switchToFileBrowser); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyF2, gocui.ModNone, switchToRegisters); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyF3, gocui.ModNone, switchToVariables); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyF4, gocui.ModNone, switchToStack); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyF5, gocui.ModNone, switchToCode); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyF6, gocui.ModNone, switchToCommand); err != nil {
		log.Panicln(err)
	}

	// F11全屏切换
	if err := g.SetKeybinding("", gocui.KeyF11, gocui.ModNone, toggleFullscreenHandler); err != nil {
		log.Panicln(err)
	}

	// ESC键退出全屏（全局绑定，优先处理全屏退出）
	if err := g.SetKeybinding("", gocui.KeyEsc, gocui.ModNone, escapeExitFullscreenHandler); err != nil {
		log.Panicln(err)
	}

	// 方向键滚动
	if err := g.SetKeybinding("", gocui.KeyArrowUp, gocui.ModNone, scrollUpHandler); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyArrowDown, gocui.ModNone, scrollDownHandler); err != nil {
		log.Panicln(err)
	}

	// PgUp/PgDn快速滚动
	if err := g.SetKeybinding("", gocui.KeyPgup, gocui.ModNone, scrollUpHandler); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", gocui.KeyPgdn, gocui.ModNone, scrollDownHandler); err != nil {
		log.Panicln(err)
	}

	// Enter键文件选择（在文件浏览器中）
	if err := g.SetKeybinding("filebrowser", gocui.KeyEnter, gocui.ModNone, handleFileSelection); err != nil {
		log.Panicln(err)
	}
	
	// Enter键设置断点（在代码视图中）
	if err := g.SetKeybinding("code", gocui.KeyEnter, gocui.ModNone, handleBreakpointToggle); err != nil {
		log.Panicln(err)
	}
	
	// Enter键处理命令（在命令窗口中）
	if err := g.SetKeybinding("command", gocui.KeyEnter, gocui.ModNone, handleCommand); err != nil {
		log.Panicln(err)
	}
	
	// 退格键支持（在命令窗口中）
	if err := g.SetKeybinding("command", gocui.KeyBackspace, gocui.ModNone, handleBackspace); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("command", gocui.KeyBackspace2, gocui.ModNone, handleBackspace); err != nil {
		log.Panicln(err)
	}
	
	// ESC键在命令窗口中的专门处理（优先级高于全局ESC绑定）
	if err := g.SetKeybinding("command", gocui.KeyEsc, gocui.ModNone, escapeExitFullscreenHandler); err != nil {
		log.Panicln(err)
	}
	
	// ESC键现在由全局处理函数统一处理（全屏退出或清空命令输入）
	
	// g键生成BPF
	if err := g.SetKeybinding("", 'g', gocui.ModNone, generateBPFHandler); err != nil {
		log.Panicln(err)
	}
	
	// c键清除断点
	if err := g.SetKeybinding("", 'c', gocui.ModNone, clearBreakpointsHandler); err != nil {
		log.Panicln(err)
	}

	// 布局调整快捷键
	// Ctrl+R 重置布局
	if err := g.SetKeybinding("", gocui.KeyCtrlR, gocui.ModNone, resetLayout); err != nil {
		log.Panicln(err)
	}
	
	// Ctrl+J 增加命令窗口高度
	if err := g.SetKeybinding("", gocui.KeyCtrlJ, gocui.ModNone, adjustCommandHeightHandler); err != nil {
		log.Panicln(err)
	}
	
	// Ctrl+K 减少命令窗口高度
	if err := g.SetKeybinding("", gocui.KeyCtrlK, gocui.ModNone, shrinkCommandHeightHandler); err != nil {
		log.Panicln(err)
	}
	
	// Ctrl+H 减少左侧面板宽度
	if err := g.SetKeybinding("", gocui.KeyCtrlH, gocui.ModNone, shrinkLeftPanelHandler); err != nil {
		log.Panicln(err)
	}
	
	// Ctrl+L 增加左侧面板宽度
	if err := g.SetKeybinding("", gocui.KeyCtrlL, gocui.ModNone, adjustLeftPanelHandler); err != nil {
		log.Panicln(err)
	}
	
	// 在命令窗口中添加常用字符的输入绑定
	// 包含所有常见的路径、文件名和命令字符
	basicChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	pathChars := "./-_:=~+()[]{}@#$%^&*,;<>?|\\`"
	allChars := basicChars + pathChars
	
	for _, ch := range allChars {
		if err := g.SetKeybinding("command", ch, gocui.ModNone, handleCharInput(ch)); err != nil {
			log.Printf("警告: 无法绑定字符 %c (ASCII %d): %v", ch, int(ch), err)
		}
	}
	
	// 单独处理空格键，确保优先级
	if err := g.SetKeybinding("command", ' ', gocui.ModNone, handleCharInput(' ')); err != nil {
		log.Printf("警告: 无法绑定空格键: %v", err)
	}

	// 鼠标事件绑定
	// 文件浏览器特殊鼠标处理：点击打开文件/展开目录
	if err := g.SetKeybinding("filebrowser", gocui.MouseLeft, gocui.ModNone, handleFileBrowserClick); err != nil {
		log.Panicln(err)
	}
	
	// 代码视图特殊鼠标处理：双击设置断点
	if err := g.SetKeybinding("code", gocui.MouseLeft, gocui.ModNone, handleCodeViewClick); err != nil {
		log.Panicln(err)
	}
	
	// 命令窗口特殊鼠标处理：点击时设置CommandDirty
	if err := g.SetKeybinding("command", gocui.MouseLeft, gocui.ModNone, handleCommandClick); err != nil {
		log.Panicln(err)
	}
	
	// 其他窗口的标准鼠标处理
	viewNames := []string{"registers", "variables", "stack"}
	
	for _, viewName := range viewNames {
		// 鼠标单击聚焦
		if err := g.SetKeybinding(viewName, gocui.MouseLeft, gocui.ModNone, mouseDownHandler); err != nil {
			log.Panicln(err)
		}
		
		// 鼠标滚轮滚动（命令窗口不需要滚动）
		if viewName != "command" {
		if err := g.SetKeybinding(viewName, gocui.MouseWheelUp, gocui.ModNone, mouseScrollUpHandler); err != nil {
			log.Panicln(err)
		}
		if err := g.SetKeybinding(viewName, gocui.MouseWheelDown, gocui.ModNone, mouseScrollDownHandler); err != nil {
			log.Panicln(err)
		}
		}
	}
	
	// 代码视图滚轮支持
	if err := g.SetKeybinding("code", gocui.MouseWheelUp, gocui.ModNone, mouseScrollUpHandler); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("code", gocui.MouseWheelDown, gocui.ModNone, mouseScrollDownHandler); err != nil {
		log.Panicln(err)
	}
	
	// 文件浏览器的滚轮支持
	if err := g.SetKeybinding("filebrowser", gocui.MouseWheelUp, gocui.ModNone, mouseScrollUpHandler); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("filebrowser", gocui.MouseWheelDown, gocui.ModNone, mouseScrollDownHandler); err != nil {
		log.Panicln(err)
	}

	// 设置信号处理
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// 启动更新协程
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		
		// 首次设置初始聚焦窗口
		firstRun := true

		for {
			select {
			case <-ticker.C:
				g.Update(func(g *gocui.Gui) error {
					// 首次运行时设置初始聚焦窗口
					if firstRun {
					if _, err := g.SetCurrentView("filebrowser"); err == nil {
							firstRun = false
						}
					}
					updateAllViews(g, ctx)
					return nil
				})
			case <-sigChan:
				g.Update(func(g *gocui.Gui) error {
					return gocui.ErrQuit
				})
				return
			}
		}
	}()

	// 运行主循环
	if err := g.MainLoop(); err != nil && err != gocui.ErrQuit {
		log.Panicln(err)
	}
}

// ========== 断点持久化功能 ==========

// 保存断点到文件
func saveBreakpoints(ctx *DebuggerContext) error {
	if ctx.Project == nil {
		return fmt.Errorf("没有打开的项目")
	}
	
	breakpointsPath := filepath.Join(ctx.Project.RootPath, ".debug_breakpoints.json")
	
	// 将断点序列化为JSON
	data, err := json.MarshalIndent(ctx.Project.Breakpoints, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化断点失败: %v", err)
	}
	
	// 写入文件
	err = ioutil.WriteFile(breakpointsPath, data, 0644)
	if err != nil {
		return fmt.Errorf("保存断点文件失败: %v", err)
	}
	
	return nil
}

// 从文件加载断点
func loadBreakpoints(ctx *DebuggerContext) error {
	if ctx.Project == nil {
		return fmt.Errorf("没有打开的项目")
	}
	
	breakpointsPath := filepath.Join(ctx.Project.RootPath, ".debug_breakpoints.json")
	
	// 检查文件是否存在
	if _, err := os.Stat(breakpointsPath); os.IsNotExist(err) {
		// 文件不存在，不是错误，只是没有保存的断点
		return nil
	}
	
	// 读取文件
	data, err := ioutil.ReadFile(breakpointsPath)
	if err != nil {
		return fmt.Errorf("读取断点文件失败: %v", err)
	}
	
	// 反序列化JSON
	var breakpoints []Breakpoint
	err = json.Unmarshal(data, &breakpoints)
	if err != nil {
		return fmt.Errorf("解析断点文件失败: %v", err)
	}
	
	// 加载断点到项目
	ctx.Project.Breakpoints = breakpoints
	
	return nil
}


