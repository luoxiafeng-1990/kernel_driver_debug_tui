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
	"debug/dwarf"
	"debug/elf"
	"regexp"

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
	// 代码搜索系统
	SearchMode     bool          // 是否处于搜索模式
	SearchTerm     string        // 当前搜索词
	SearchResults  []SearchResult // 搜索结果列表
	CurrentMatch   int           // 当前匹配项索引
	SearchInput    string        // 搜索输入缓冲区
	SearchDirty    bool          // 搜索结果是否需要更新
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

// 搜索结果结构
type SearchResult struct {
	LineNumber  int // 行号（从1开始）
	StartColumn int // 匹配开始列（从0开始）
	EndColumn   int // 匹配结束列（从0开始）
	Text        string // 匹配的文本
}

// DWARF变量位置信息
type VariableLocation struct {
	Name        string
	Type        string // "register", "stack", "memory"
	Register    string // 寄存器名称 (如 "rax", "rbx")
	StackOffset int    // 栈偏移量
	Size        int    // 变量大小
}

// BPF支持的架构映射
var SupportedArchitectures = map[string]string{
	"x86_64":  "__TARGET_ARCH_x86",
	"aarch64": "__TARGET_ARCH_arm64", 
	"arm64":   "__TARGET_ARCH_arm64",
	"riscv64": "__TARGET_ARCH_riscv",
	"s390x":   "__TARGET_ARCH_s390",
	"ppc64le": "__TARGET_ARCH_powerpc",
	"mips64":  "__TARGET_ARCH_mips",
}

// 架构显示名称映射
var ArchDisplayNames = map[string]string{
	"x86_64":  "x86_64 (Intel/AMD 64-bit)",
	"aarch64": "ARM64/AArch64",
	"arm64":   "ARM64/AArch64", 
	"riscv64": "RISC-V 64-bit",
	"s390x":   "IBM System z",
	"ppc64le": "PowerPC 64-bit LE",
	"mips64":  "MIPS 64-bit",
}

// 检测当前系统架构
func detectCurrentArch() string {
	output, err := exec.Command("uname", "-m").Output()
	if err != nil {
		return "x86_64" // 默认架构
	}
	
	arch := strings.TrimSpace(string(output))
	
	// 标准化架构名称
	switch arch {
	case "x86_64", "amd64":
		return "x86_64"
	case "aarch64", "arm64":
		return "aarch64"
	case "riscv64":
		return "riscv64"
	case "s390x":
		return "s390x"
	case "ppc64le":
		return "ppc64le"
	case "mips64":
		return "mips64"
	default:
		return "x86_64" // 默认使用x86_64
	}
}

// 注意：selectTargetArchitecture 函数已废弃
// 现在使用命令行参数方式进行架构选择，避免TUI环境下的输入冲突

var (
	focusNames = []string{"File Browser", "Registers", "Variables", "Call Stack", "Code View", "Memory", "Command"}
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
			v.Title = "File Browser [Fullscreen] - F11/ESC to Exit"
		case "code":
			v.Title = "Code View [Fullscreen] - F11/ESC to Exit"
		case "registers":
			v.Title = "Registers [Fullscreen] - F11/ESC to Exit"
		case "variables":
			v.Title = "Variables [Fullscreen] - F11/ESC to Exit"
		case "stack":
			v.Title = "Call Stack [Fullscreen] - F11/ESC to Exit"
		case "command":
			v.Title = "Command [Fullscreen] - F11/ESC to Exit"
			v.Editable = true
			v.Wrap = false
		default:
			v.Title = fmt.Sprintf("%s [Fullscreen] - F11/ESC to Exit", viewName)
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
					debugMsg := fmt.Sprintf("[ERROR] Failed to close current popup window with ESC: %s, error: %v", popupID, err)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
	} else {
		debugMsg := fmt.Sprintf("[DEBUG] Successfully closed current popup window with ESC: %s", popupID)
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
			debugMsg := fmt.Sprintf("[ERROR] Failed to close popup window with ESC: %s, error: %v", lastPopup.ID, err)
			globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		} else {
			// 调试信息
			debugMsg := fmt.Sprintf("[DEBUG] Successfully closed popup window with ESC: %s", lastPopup.ID)
			globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		}
		globalCtx.CommandDirty = true
		
		return nil
	}
	
	// 只有在全屏状态下才处理ESC键退出全屏
	if globalCtx.IsFullscreen {
		// 调试信息
		debugMsg := fmt.Sprintf("[DEBUG] ESC key exit fullscreen: current view=%s, fullscreen view=%s", currentView, globalCtx.FullscreenView)
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
		debugMsg := fmt.Sprintf("[DEBUG] ESC key clear command input: current input=%s", globalCtx.CurrentInput)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
		globalCtx.CommandDirty = true
		
		return clearCurrentInput(g, v)
	}
	
	// 其他情况的调试信息
	debugMsg := fmt.Sprintf("[DEBUG] ESC key no action: view=%s, fullscreen status=%v", currentView, globalCtx.IsFullscreen)
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
	
	// 检查最小终端尺寸
	minWidth, minHeight := 120, 30
	if maxX < minWidth || maxY < minHeight {
		// 如果终端太小，显示错误信息
		if v, err := g.SetView("error", 0, 0, maxX-1, maxY-1); err != nil {
			if err != gocui.ErrUnknownView {
				return err
			}
			v.Title = "Terminal Too Small"
		}
		if v, _ := g.View("error"); v != nil {
			v.Clear()
			fmt.Fprintf(v, "\n")
			fmt.Fprintf(v, "  Terminal window is too small!\n")
			fmt.Fprintf(v, "\n")
			fmt.Fprintf(v, "  Current size: %dx%d\n", maxX, maxY)
			fmt.Fprintf(v, "  Required: %dx%d or larger\n", minWidth, minHeight)
			fmt.Fprintf(v, "\n")
			fmt.Fprintf(v, "  Please resize your terminal and try again.\n")
			fmt.Fprintf(v, "  Press Ctrl+C to exit.\n")
		}
		return nil
	}
	
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
		v.Title = "Status"
	}
	
	// 文件浏览器窗口 (左侧) - 使用安全的底部坐标
	if v, err := g.SetView("filebrowser", 0, 3, layout.LeftPanelWidth, safeBottomY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "File Browser"
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
		v.Title = "Code View"
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
		v.Title = "Registers"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 变量窗口 (右中) - 使用安全的分割点
	if v, err := g.SetView("variables", rightStartX, layout.RightPanelSplit1+1, maxX-1, layout.RightPanelSplit2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "Variables"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 调用栈窗口 (右下) - 使用安全的底部坐标
	if v, err := g.SetView("stack", rightStartX, layout.RightPanelSplit2+1, maxX-1, safeBottomY); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "Call Stack"
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
		v.Title = "Command"
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
		return nil, fmt.Errorf("Project directory does not exist: %s", projectPath)
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
		// 静默处理，不输出到终端
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
				ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("[ERROR] Failed to save breakpoints: %v", err))
				ctx.CommandDirty = true
			}
			return
		}
	}
	
	// 解析函数名
	functionName := parseFunctionName(file, line)
	if functionName == "" {
		functionName = "unknown"
	}
	
	// 添加新断点
	bp := Breakpoint{
		File:     file,
		Line:     line,
		Function: functionName, // 使用解析出的函数名
		Enabled:  true,
	}
	ctx.Project.Breakpoints = append(ctx.Project.Breakpoints, bp)
	
	// 保存断点到文件
	if err := saveBreakpoints(ctx); err != nil {
		// 在命令历史中记录错误
		ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("[ERROR] Failed to save breakpoints: %v", err))
		ctx.CommandDirty = true
	}
}

// 从C源码中解析指定行所在的函数名
func parseFunctionName(filePath string, targetLine int) string {
	// 读取文件内容
	lines, err := readFileContent(filePath)
	if err != nil {
		return ""
	}
	
	if targetLine <= 0 || targetLine > len(lines) {
		return ""
	}
	
	// 从目标行向上查找函数定义
	var currentFunction string
	
	for i := targetLine - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		
		// 跳过空行和注释
		if line == "" || strings.HasPrefix(line, "//") || strings.HasPrefix(line, "/*") {
			continue
		}
		
		// 查找函数定义模式
		// 匹配：返回类型 函数名(参数) 或 函数名(参数)
		if funcName := extractFunctionName(line); funcName != "" {
			currentFunction = funcName
			// 继续向上查找，确保找到最近的函数定义
		}
		
		// 如果遇到 } 说明退出了当前函数作用域
		if strings.Contains(line, "}") && !strings.Contains(line, "{") {
			break
		}
	}
	
	return currentFunction
}

// 从一行代码中提取函数名
func extractFunctionName(line string) string {
	// 移除多余的空格
	line = strings.TrimSpace(line)
	
	// 常见的C函数定义模式
	patterns := []string{
		// static int function_name(
		// int function_name(
		// void function_name(
		`^(static\s+)?([\w\s\*]+)\s+(\w+)\s*\(`,
		// function_name( (无返回类型声明的情况)
		`^(\w+)\s*\(`,
	}
	
	for _, pattern := range patterns {
		if matched, funcName := matchFunctionPattern(line, pattern); matched {
			// 过滤掉一些常见的非函数关键字
			if !isValidFunctionName(funcName) {
				continue
			}
			return funcName
		}
	}
	
	return ""
}

// 使用正则表达式匹配函数模式（Go 1.13兼容的简化版本）
func matchFunctionPattern(line, pattern string) (bool, string) {
	// 简化的模式匹配，避免使用复杂的正则表达式
	
	// 模式1: 标准函数定义 "type function_name("
	if strings.Contains(line, "(") && !strings.Contains(line, "if") && 
	   !strings.Contains(line, "while") && !strings.Contains(line, "for") {
		
		// 查找 ( 的位置
		parenIdx := strings.Index(line, "(")
		if parenIdx == -1 {
			return false, ""
		}
		
		// 提取 ( 之前的部分
		beforeParen := strings.TrimSpace(line[:parenIdx])
		
		// 按空格分割，最后一个词应该是函数名
		parts := strings.Fields(beforeParen)
		if len(parts) == 0 {
			return false, ""
		}
		
		funcName := parts[len(parts)-1]
		
		// 移除可能的指针符号
		funcName = strings.TrimLeft(funcName, "*")
		
		return true, funcName
	}
	
	return false, ""
}

// 检查是否是有效的函数名
func isValidFunctionName(name string) bool {
	// 过滤掉C关键字和常见的非函数标识符
	invalidNames := map[string]bool{
		"if":       true,
		"else":     true,
		"while":    true,
		"for":      true,
		"switch":   true,
		"case":     true,
		"return":   true,
		"break":    true,
		"continue": true,
		"sizeof":   true,
		"typedef":  true,
		"struct":   true,
		"union":    true,
		"enum":     true,
		"const":    true,
		"static":   true,
		"extern":   true,
		"inline":   true,
		"int":      true,
		"char":     true,
		"void":     true,
		"long":     true,
		"short":    true,
		"unsigned": true,
		"signed":   true,
		"float":    true,
		"double":   true,
	}
	
	// 检查长度
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	
	// 检查是否是关键字
	if invalidNames[strings.ToLower(name)] {
		return false
	}
	
	// 检查是否以数字开头
	if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		return false
	}
	
	// 检查是否包含有效字符
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || 
		     (r >= '0' && r <= '9') || r == '_') {
			return false
		}
	}
	
	return true
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
	
	// 检测当前架构并生成对应的定义
	currentArch := detectCurrentArch()
	archDefine, exists := SupportedArchitectures[currentArch]
	if !exists {
		archDefine = "__TARGET_ARCH_x86" // 默认架构
	}

	// 写入BPF代码头部
	fmt.Fprintln(file, "#include <linux/bpf.h>")
	fmt.Fprintln(file, "#include <bpf/bpf_helpers.h>")
	fmt.Fprintln(file, "#include <bpf/bpf_tracing.h>")
	fmt.Fprintln(file, "#include <linux/ptrace.h>")
	fmt.Fprintln(file, "#include <linux/types.h>")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "// 定义目标架构 - 解决PT_REGS_PARM错误")
	fmt.Fprintf(file, "#define %s\n", archDefine)
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "// 自动生成的BPF调试代码")
	fmt.Fprintln(file, "// 生成时间:", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintln(file, "")
	
	// 添加类型定义（兼容性处理）
	fmt.Fprintln(file, "// 类型定义（确保兼容性）")
	fmt.Fprintln(file, "#ifndef u32")
	fmt.Fprintln(file, "typedef __u32 u32;")
	fmt.Fprintln(file, "#endif")
	fmt.Fprintln(file, "#ifndef u64")
	fmt.Fprintln(file, "typedef __u64 u64;")
	fmt.Fprintln(file, "#endif")
	fmt.Fprintln(file, "")
	
	// 添加调试上下文结构
	fmt.Fprintln(file, "// 调试事件结构")
	fmt.Fprintln(file, "struct debug_event {")
	fmt.Fprintln(file, "    u32 pid;")
	fmt.Fprintln(file, "    u32 tgid;") 
	fmt.Fprintln(file, "    u64 timestamp;")
	fmt.Fprintln(file, "    u32 breakpoint_id;")
	fmt.Fprintln(file, "    char comm[16];")
	fmt.Fprintln(file, "    char function[64];")
	fmt.Fprintln(file, "};")
	fmt.Fprintln(file, "")
	
	// 为每个启用的断点生成探针
	validBreakpoints := 0
	for i, bp := range ctx.Project.Breakpoints {
		if !bp.Enabled {
			continue
		}
		
		funcName := bp.Function
		if funcName == "unknown" || funcName == "" {
			// 尝试重新解析函数名
			if parsedName := parseFunctionName(bp.File, bp.Line); parsedName != "" {
				funcName = parsedName
				// 更新断点中的函数名
				ctx.Project.Breakpoints[i].Function = funcName
			} else {
				// 跳过无法确定函数名的断点
				continue
			}
		}
		
		fileName := filepath.Base(bp.File)
		
		fmt.Fprintf(file, "// 断点 %d: %s:%d 在函数 %s\n", validBreakpoints+1, fileName, bp.Line, funcName)
		fmt.Fprintf(file, "SEC(\"kprobe/%s\")\n", funcName)
		fmt.Fprintf(file, "int trace_breakpoint_%d(struct pt_regs *ctx) {\n", validBreakpoints)
		fmt.Fprintln(file, "    struct debug_event event = {};")
		fmt.Fprintln(file, "    ")
		fmt.Fprintln(file, "    // 获取进程信息")
		fmt.Fprintln(file, "    u64 pid_tgid = bpf_get_current_pid_tgid();")
		fmt.Fprintln(file, "    event.pid = pid_tgid;")
		fmt.Fprintln(file, "    event.tgid = pid_tgid >> 32;")
		fmt.Fprintln(file, "    event.timestamp = bpf_ktime_get_ns();")
		fmt.Fprintf(file, "    event.breakpoint_id = %d;\n", validBreakpoints)
		fmt.Fprintln(file, "    bpf_get_current_comm(&event.comm, sizeof(event.comm));")
		fmt.Fprintf(file, "    bpf_probe_read_str(&event.function, sizeof(event.function), \"%s\");\n", funcName)
		fmt.Fprintln(file, "    ")
		fmt.Fprintf(file, "    // 打印调试信息\n")
		fmt.Fprintf(file, "    bpf_printk(\"[BREAKPOINT-%d] %s:%d in %%s() PID=%%d\\n\", \"%s\", event.pid);\n", 
			validBreakpoints+1, fileName, bp.Line, funcName)
		fmt.Fprintln(file, "    ")
		fmt.Fprintln(file, "    // TODO: 将事件发送到用户空间")
		fmt.Fprintln(file, "    // bpf_perf_event_output(ctx, &events, BPF_F_CURRENT_CPU, &event, sizeof(event));")
		fmt.Fprintln(file, "    ")
		fmt.Fprintln(file, "    return 0;")
		fmt.Fprintln(file, "}")
		fmt.Fprintln(file, "")
		
		validBreakpoints++
	}
	
	if validBreakpoints == 0 {
		return fmt.Errorf("没有找到有效的函数名，无法生成BPF探针")
	}
	
	fmt.Fprintln(file, "char LICENSE[] SEC(\"license\") = \"GPL\";")
	
	// 生成编译和加载脚本
	scriptPath := filepath.Join(ctx.Project.RootPath, "load_debug_bpf.sh")
	if err := generateLoadScript(scriptPath, validBreakpoints); err != nil {
		return fmt.Errorf("生成加载脚本失败: %v", err)
	}
	
	// 生成卸载脚本  
	unloadScriptPath := filepath.Join(ctx.Project.RootPath, "unload_debug_bpf.sh")
	if err := generateUnloadScript(unloadScriptPath); err != nil {
		return fmt.Errorf("生成卸载脚本失败: %v", err)
	}
	
	// 保存更新后的断点信息（包含解析出的函数名）
	if err := saveBreakpoints(ctx); err != nil {
		// 这不是致命错误，只记录警告
		ctx.CommandHistory = append(ctx.CommandHistory, fmt.Sprintf("[WARNING] Failed to save breakpoints: %v", err))
	}
	
	return nil
}

// 生成BPF加载脚本
func generateLoadScript(scriptPath string, breakpointCount int) error {
	file, err := os.Create(scriptPath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	// 设置可执行权限
	os.Chmod(scriptPath, 0755)
	
	fmt.Fprintln(file, "#!/bin/bash")
	fmt.Fprintln(file, "# 自动生成的BPF调试程序加载脚本")
	fmt.Fprintln(file, "# 生成时间:", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "set -e  # 遇到错误立即退出")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "BPF_FILE=\"debug_breakpoints.bpf.c\"")
	fmt.Fprintln(file, "BPF_OBJ=\"debug_breakpoints.bpf.o\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "echo \"[INFO] 开始编译和加载BPF调试程序...\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 检查是否有root权限")
	fmt.Fprintln(file, "if [ \"$EUID\" -ne 0 ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] 需要root权限来加载BPF程序\"")
	fmt.Fprintln(file, "    echo \"请使用: sudo $0\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 检查BPF源文件是否存在")
	fmt.Fprintln(file, "if [ ! -f \"$BPF_FILE\" ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] BPF源文件 $BPF_FILE 不存在\"")
	fmt.Fprintln(file, "    echo \"请先运行调试器并使用generate命令生成BPF代码\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 编译BPF程序")
	fmt.Fprintln(file, "echo \"[INFO] 编译BPF程序...\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 检测架构并设置include路径")
	fmt.Fprintln(file, "ARCH=$(uname -m)")
	fmt.Fprintln(file, "INCLUDE_FLAGS=\"\"")
	fmt.Fprintln(file, "case \"$ARCH\" in")
	fmt.Fprintln(file, "    riscv64)")
	fmt.Fprintln(file, "        INCLUDE_FLAGS=\"-I/usr/include/riscv64-linux-gnu -I/usr/include\"")
	fmt.Fprintln(file, "        ;;")
	fmt.Fprintln(file, "    aarch64)")
	fmt.Fprintln(file, "        INCLUDE_FLAGS=\"-I/usr/include/aarch64-linux-gnu -I/usr/include\"")
	fmt.Fprintln(file, "        ;;")
	fmt.Fprintln(file, "    x86_64)")
	fmt.Fprintln(file, "        INCLUDE_FLAGS=\"-I/usr/include/x86_64-linux-gnu -I/usr/include\"")
	fmt.Fprintln(file, "        ;;")
	fmt.Fprintln(file, "    *)")
	fmt.Fprintln(file, "        INCLUDE_FLAGS=\"-I/usr/include\"")
	fmt.Fprintln(file, "        ;;")
	fmt.Fprintln(file, "esac")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "echo \"[INFO] 架构: $ARCH\"")
	fmt.Fprintln(file, "echo \"[INFO] Include参数: $INCLUDE_FLAGS\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "clang -O2 -target bpf $INCLUDE_FLAGS -c \"$BPF_FILE\" -o \"$BPF_OBJ\"")
	fmt.Fprintln(file, "if [ $? -ne 0 ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] BPF程序编译失败\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 加载BPF程序")
	fmt.Fprintln(file, "echo \"[INFO] 加载BPF程序...\"")
	fmt.Fprintln(file, "bpftool prog load \"$BPF_OBJ\" /sys/fs/bpf/debug_breakpoints")
	fmt.Fprintln(file, "if [ $? -ne 0 ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] BPF程序加载失败\"")
	fmt.Fprintln(file, "    echo \"请检查:\"")
	fmt.Fprintln(file, "    echo \"1. 是否安装了bpftool\"")
	fmt.Fprintln(file, "    echo \"2. 内核是否支持BPF\"")
	fmt.Fprintln(file, "    echo \"3. 目标函数是否存在于内核中\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintf(file, "echo \"[SUCCESS] BPF调试程序已加载，监控 %d 个断点\"\n", breakpointCount)
	fmt.Fprintln(file, "echo \"[INFO] 使用以下命令查看调试输出:\"")
	fmt.Fprintln(file, "echo \"  sudo cat /sys/kernel/debug/tracing/trace_pipe\"")
	fmt.Fprintln(file, "echo \"[INFO] 使用以下命令卸载:\"")
	fmt.Fprintln(file, "echo \"  sudo ./unload_debug_bpf.sh\"")
	
	return nil
}

// 生成BPF卸载脚本
func generateUnloadScript(scriptPath string) error {
	file, err := os.Create(scriptPath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	// 设置可执行权限
	os.Chmod(scriptPath, 0755)
	
	fmt.Fprintln(file, "#!/bin/bash")
	fmt.Fprintln(file, "# BPF调试程序卸载脚本")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "echo \"[INFO] 卸载BPF调试程序...\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 检查是否有root权限")
	fmt.Fprintln(file, "if [ \"$EUID\" -ne 0 ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] 需要root权限来卸载BPF程序\"")
	fmt.Fprintln(file, "    echo \"请使用: sudo $0\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 卸载BPF程序")
	fmt.Fprintln(file, "rm -f /sys/fs/bpf/debug_breakpoints")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 清理编译产物")
	fmt.Fprintln(file, "rm -f debug_breakpoints.bpf.o")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "echo \"[SUCCESS] BPF调试程序已卸载\"")
	
	return nil
}

// 生成变量监控BPF加载脚本
func generateVarsLoadScript(scriptPath string, breakpointCount int) error {
	file, err := os.Create(scriptPath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	// 设置可执行权限
	os.Chmod(scriptPath, 0755)
	
	fmt.Fprintln(file, "#!/bin/bash")
	fmt.Fprintln(file, "# 变量监控BPF程序加载脚本")
	fmt.Fprintln(file, "# 生成时间:", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "set -e")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "BPF_FILE=\"debug_variables.bpf.c\"")
	fmt.Fprintln(file, "BPF_OBJ=\"debug_variables.bpf.o\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "echo \"[INFO] 🔥 Loading Variable Monitoring BPF Program...\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 检查root权限")
	fmt.Fprintln(file, "if [ \"$EUID\" -ne 0 ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] Root privileges required\"")
	fmt.Fprintln(file, "    echo \"Please run: sudo $0\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 检查BPF源文件")
	fmt.Fprintln(file, "if [ ! -f \"$BPF_FILE\" ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] BPF source file $BPF_FILE not found\"")
	fmt.Fprintln(file, "    echo \"Please run 'vars' command in debugger first\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 编译BPF程序")
	fmt.Fprintln(file, "echo \"[INFO] Compiling variable monitoring BPF program...\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 检测架构并设置include路径")
	fmt.Fprintln(file, "ARCH=$(uname -m)")
	fmt.Fprintln(file, "INCLUDE_FLAGS=\"\"")
	fmt.Fprintln(file, "case \"$ARCH\" in")
	fmt.Fprintln(file, "    riscv64)")
	fmt.Fprintln(file, "        INCLUDE_FLAGS=\"-I/usr/include/riscv64-linux-gnu -I/usr/include\"")
	fmt.Fprintln(file, "        ;;")
	fmt.Fprintln(file, "    aarch64)")
	fmt.Fprintln(file, "        INCLUDE_FLAGS=\"-I/usr/include/aarch64-linux-gnu -I/usr/include\"")
	fmt.Fprintln(file, "        ;;")
	fmt.Fprintln(file, "    x86_64)")
	fmt.Fprintln(file, "        INCLUDE_FLAGS=\"-I/usr/include/x86_64-linux-gnu -I/usr/include\"")
	fmt.Fprintln(file, "        ;;")
	fmt.Fprintln(file, "    *)")
	fmt.Fprintln(file, "        INCLUDE_FLAGS=\"-I/usr/include\"")
	fmt.Fprintln(file, "        ;;")
	fmt.Fprintln(file, "esac")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "echo \"[INFO] Architecture: $ARCH\"")
	fmt.Fprintln(file, "echo \"[INFO] Include flags: $INCLUDE_FLAGS\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "clang -O2 -target bpf $INCLUDE_FLAGS -c \"$BPF_FILE\" -o \"$BPF_OBJ\"")
	fmt.Fprintln(file, "if [ $? -ne 0 ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] BPF compilation failed\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 加载BPF程序")
	fmt.Fprintln(file, "echo \"[INFO] Loading variable monitoring BPF program...\"")
	fmt.Fprintln(file, "bpftool prog load \"$BPF_OBJ\" /sys/fs/bpf/debug_variables")
	fmt.Fprintln(file, "if [ $? -ne 0 ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] BPF program loading failed\"")
	fmt.Fprintln(file, "    echo \"Please check:\"")
	fmt.Fprintln(file, "    echo \"• bpftool installation\"")
	fmt.Fprintln(file, "    echo \"• Kernel BPF support\"")
	fmt.Fprintln(file, "    echo \"• Target functions exist in kernel\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintf(file, "echo \"[SUCCESS] 🎯 Variable monitoring active for %d breakpoints\"\n", breakpointCount)
	fmt.Fprintln(file, "echo \"\"")
	fmt.Fprintln(file, "echo \"📊 View real-time variable monitoring:\"")
	fmt.Fprintln(file, "echo \"  sudo cat /sys/kernel/debug/tracing/trace_pipe\"")
	fmt.Fprintln(file, "echo \"\"")
	fmt.Fprintln(file, "echo \"🛑 To stop monitoring:\"")
	fmt.Fprintln(file, "echo \"  sudo ./unload_debug_vars.sh\"")
	
	return nil
}

// 生成变量监控BPF卸载脚本
func generateVarsUnloadScript(scriptPath string) error {
	file, err := os.Create(scriptPath)
	if err != nil {
		return err
	}
	defer file.Close()
	
	// 设置可执行权限
	os.Chmod(scriptPath, 0755)
	
	fmt.Fprintln(file, "#!/bin/bash")
	fmt.Fprintln(file, "# 变量监控BPF程序卸载脚本")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "echo \"[INFO] 🛑 Unloading variable monitoring BPF program...\"")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 检查root权限")
	fmt.Fprintln(file, "if [ \"$EUID\" -ne 0 ]; then")
	fmt.Fprintln(file, "    echo \"[ERROR] Root privileges required\"")
	fmt.Fprintln(file, "    echo \"Please run: sudo $0\"")
	fmt.Fprintln(file, "    exit 1")
	fmt.Fprintln(file, "fi")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 卸载BPF程序")
	fmt.Fprintln(file, "rm -f /sys/fs/bpf/debug_variables")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 清理编译产物")
	fmt.Fprintln(file, "rm -f debug_variables.bpf.o")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "echo \"[SUCCESS] ✅ Variable monitoring stopped and cleaned up\"")
	
	return nil
}

// 自动解析函数中的所有变量（新功能）
func parseAllFunctionVariables(filePath string, lineNumber int) []string {
	// 首先尝试从源码中解析
	if vars := parseVariablesFromSource(filePath, lineNumber); len(vars) > 0 {
		return vars
	}
	
	// 回退到DWARF解析（如果有调试信息）
	if vars := parseVariablesFromDWARF(filePath, lineNumber); len(vars) > 0 {
		return vars
	}
	
	// 最后回退到常见变量模式
	return []string{"local_var", "counter", "temp", "i", "len", "ret", "addr", "ptr", "data", "size", "index", "val", "result"}
}

// 从源码中解析函数的所有局部变量
func parseVariablesFromSource(filePath string, targetLine int) []string {
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return nil
	}
	
	lines := strings.Split(string(content), "\n")
	if targetLine > len(lines) {
		return nil
	}
	
	// 找到目标行所在的函数
	functionStart, functionEnd := findFunctionBounds(lines, targetLine-1) // 转换为0基索引
	if functionStart == -1 || functionEnd == -1 {
		return nil
	}
	
	var variables []string
	variableSet := make(map[string]bool) // 去重
	
	// 解析函数内的所有变量声明
	for i := functionStart; i <= functionEnd && i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if vars := extractVariablesFromLine(line); len(vars) > 0 {
			for _, v := range vars {
				if !variableSet[v] && isValidFunctionName(v) { // 复用现有的验证函数
					variables = append(variables, v)
					variableSet[v] = true
				}
			}
		}
	}
	
	return variables
}

// 找到函数的开始和结束行
func findFunctionBounds(lines []string, targetLine int) (int, int) {
	if targetLine >= len(lines) {
		return -1, -1
	}
	
	functionStart := -1
	functionEnd := -1
	braceLevel := 0
	
	// 向上搜索函数开始
	for i := targetLine; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		
		// 检查是否是函数定义行
		if strings.Contains(line, "(") && strings.Contains(line, ")") && 
		   (strings.Contains(line, "{") || (i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "{")) {
			// 简单的函数识别：包含参数列表且后面有大括号
			if !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "*") && 
			   !strings.Contains(line, "if") && !strings.Contains(line, "for") && 
			   !strings.Contains(line, "while") && !strings.Contains(line, "switch") {
				functionStart = i
				break
			}
		}
	}
	
	if functionStart == -1 {
		return -1, -1
	}
	
	// 从函数开始处向下搜索函数结束
	for i := functionStart; i < len(lines); i++ {
		line := lines[i]
		for _, ch := range line {
			if ch == '{' {
				braceLevel++
			} else if ch == '}' {
				braceLevel--
				if braceLevel == 0 {
					functionEnd = i
					return functionStart, functionEnd
				}
			}
		}
	}
	
	return functionStart, functionEnd
}

// 从单行代码中提取变量声明
func extractVariablesFromLine(line string) []string {
	var variables []string
	
	// 移除注释
	if idx := strings.Index(line, "//"); idx != -1 {
		line = line[:idx]
	}
	if idx := strings.Index(line, "/*"); idx != -1 {
		if endIdx := strings.Index(line[idx:], "*/"); endIdx != -1 {
			line = line[:idx] + line[idx+endIdx+2:]
		} else {
			line = line[:idx]
		}
	}
	
	line = strings.TrimSpace(line)
	if line == "" {
		return variables
	}
	
	// 常见的C变量声明模式
	patterns := []string{
		// 基本类型声明
		`(int|char|long|short|float|double|void|size_t|uint32_t|uint64_t|u32|u64)\s+\*?\s*(\w+)`,
		// 结构体/联合体声明
		`(struct|union)\s+\w+\s+\*?\s*(\w+)`,
		// 简单赋值（可能是声明）
		`(\w+)\s*=\s*`,
	}
	
	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		matches := re.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			if len(match) >= 3 {
				varName := strings.TrimSpace(match[len(match)-1])
				if varName != "" && !isKeyword(varName) {
					variables = append(variables, varName)
				}
			}
		}
	}
	
	return variables
}

// 检查是否是C关键字
func isKeyword(word string) bool {
	keywords := map[string]bool{
		"if": true, "else": true, "while": true, "for": true, "do": true,
		"switch": true, "case": true, "default": true, "break": true, "continue": true,
		"return": true, "goto": true, "sizeof": true, "typedef": true,
		"struct": true, "union": true, "enum": true, "const": true, "static": true,
		"extern": true, "inline": true, "volatile": true, "register": true,
		"int": true, "char": true, "void": true, "long": true, "short": true,
		"unsigned": true, "signed": true, "float": true, "double": true,
	}
	return keywords[word]
}

// 从DWARF信息中解析变量（更高级的实现）
func parseVariablesFromDWARF(filePath string, lineNumber int) []string {
	// 这里可以实现真正的DWARF解析
	// 暂时返回空，因为需要复杂的DWARF解析逻辑
	return nil
}

// 解析DWARF调试信息获取局部变量位置
func parseDWARFVariableLocations(filePath string, lineNumber int, varNames []string) map[string]VariableLocation {
	locations := make(map[string]VariableLocation)
	
	// 尝试真正的DWARF解析
	if realLocations := parseRealDWARF(filePath, lineNumber, varNames); len(realLocations) > 0 {
		return realLocations
	}
	
	// 回退到模式匹配（保持向后兼容）
	commonLocations := map[string]VariableLocation{
		"local_var": {
			Name:        "local_var",
			Type:        "stack",
			StackOffset: -8,  // rbp-8
			Size:        4,
		},
		"counter": {
			Name:     "counter",
			Type:     "register", 
			Register: "rax",
			Size:     4,
		},
		"temp": {
			Name:        "temp",
			Type:        "stack",
			StackOffset: -16,  // rbp-16
			Size:        8,
		},
		"ptr": {
			Name:     "ptr",
			Type:     "register",
			Register: "rbx",
			Size:     8,
		},
		// 添加更多常见变量模式 - RISC-V友好
		"i": {
			Name:        "i",
			Type:        "stack",
			StackOffset: -4,
			Size:        4,
		},
		"len": {
			Name:        "len",
			Type:        "stack", 
			StackOffset: -12,
			Size:        4,
		},
		"ret": {
			Name:     "ret",
			Type:     "register",
			Register: "x10", // RISC-V的返回值寄存器
			Size:     8,
		},
		"addr": {
			Name:     "addr",
			Type:     "register",
			Register: "x11",
			Size:     8,
		},
	}
	
	// 返回请求的变量位置
	for _, varName := range varNames {
		if loc, exists := commonLocations[varName]; exists {
			locations[varName] = loc
		}
	}
	
	return locations
}

// 真正的DWARF解析实现
func parseRealDWARF(binaryPath string, lineNumber int, varNames []string) map[string]VariableLocation {
	locations := make(map[string]VariableLocation)
	
	// 检查是否为ELF文件并且存在
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		return locations
	}
	
	// 使用Go标准库解析DWARF
	file, err := elf.Open(binaryPath)
	if err != nil {
		return locations
	}
	defer file.Close()
	
	// 获取DWARF数据
	dwarfData, err := file.DWARF()
	if err != nil {
		return locations
	}
	
	// 遍历DWARF编译单元
	reader := dwarfData.Reader()
	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}
		
		// 查找函数
		if entry.Tag == dwarf.TagSubprogram {
			if funcLocations := parseFunctionVariables(dwarfData, entry, lineNumber, varNames); len(funcLocations) > 0 {
				// 合并找到的变量位置
				for k, v := range funcLocations {
					locations[k] = v
				}
			}
		}
	}
	
	return locations
}

// 解析函数内的变量
func parseFunctionVariables(dwarfData *dwarf.Data, funcEntry *dwarf.Entry, lineNumber int, varNames []string) map[string]VariableLocation {
	locations := make(map[string]VariableLocation)
	
	// 获取函数的行号范围
	lowPC, _ := funcEntry.Val(dwarf.AttrLowpc).(uint64)
	_ = funcEntry.Val(dwarf.AttrHighpc).(uint64) // highPC for potential future use
	
	if lowPC == 0 {
		return locations
	}
	
	// 创建子reader来遍历函数内的变量
	reader := dwarfData.Reader()
	reader.Seek(funcEntry.Offset)
	
	// 跳过函数entry本身
	reader.Next()
	
	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}
		
		// 如果遇到另一个函数或退出当前函数作用域
		if entry.Tag == dwarf.TagSubprogram || entry.Tag == 0 {
			break
		}
		
		// 查找变量和参数
		if entry.Tag == dwarf.TagVariable || entry.Tag == dwarf.TagFormalParameter {
			if varLoc := parseVariableEntry(entry, varNames); varLoc != nil {
				locations[varLoc.Name] = *varLoc
			}
		}
	}
	
	return locations
}

// 解析单个变量entry
func parseVariableEntry(entry *dwarf.Entry, varNames []string) *VariableLocation {
	// 获取变量名
	nameAttr := entry.Val(dwarf.AttrName)
	if nameAttr == nil {
		return nil
	}
	
	varName := nameAttr.(string)
	
	// 检查是否为请求的变量
	found := false
	for _, requestedName := range varNames {
		if requestedName == varName {
			found = true
			break
		}
	}
	if !found {
		return nil
	}
	
	// 获取变量位置信息
	locationAttr := entry.Val(dwarf.AttrLocation)
	if locationAttr == nil {
		return nil
	}
	
	// 解析位置表达式
	location := parseLocationExpression(locationAttr)
	if location == nil {
		return nil
	}
	
	// 获取变量大小
	typeOffset := entry.Val(dwarf.AttrType)
	size := 8 // 默认大小
	_ = typeOffset // typeOffset for potential future use
	
	location.Name = varName
	location.Size = size
	
	return location
}

// 解析DWARF位置表达式
func parseLocationExpression(locationData interface{}) *VariableLocation {
	// DWARF位置表达式可能是byte slice
	bytes, ok := locationData.([]byte)
	if !ok || len(bytes) == 0 {
		return nil
	}
	
	// 解析第一个操作码
	opcode := bytes[0]
	
	switch opcode {
	case 0x91: // DW_OP_fbreg (frame base register offset)
		if len(bytes) >= 2 {
			// 读取LEB128编码的偏移量
			offset := int(int8(bytes[1])) // 简化处理，假设是单字节
			return &VariableLocation{
				Type:        "stack",
				StackOffset: offset,
			}
		}
		
	case 0x50, 0x51, 0x52, 0x53, 0x54, 0x55, 0x56, 0x57: // DW_OP_reg0 through DW_OP_reg7
		regNum := int(opcode - 0x50)
		regName := getRISCVRegisterName(regNum)
		return &VariableLocation{
			Type:     "register",
			Register: regName,
		}
		
	case 0x70, 0x71, 0x72, 0x73, 0x74, 0x75, 0x76, 0x77: // DW_OP_breg0 through DW_OP_breg7
		regNum := int(opcode - 0x70)
		regName := getRISCVRegisterName(regNum)
		if len(bytes) >= 2 {
			offset := int(int8(bytes[1]))
			return &VariableLocation{
				Type:        "stack",
				Register:    regName,
				StackOffset: offset,
			}
		}
	}
	
	return nil
}

// 获取RISC-V寄存器名称
func getRISCVRegisterName(regNum int) string {
	// RISC-V寄存器映射 - ABI名称
	riscvRegs := []string{
		"x0",  "x1",  "x2",  "x3",  "x4",  "x5",  "x6",  "x7",
		"x8",  "x9",  "x10", "x11", "x12", "x13", "x14", "x15",
		"x16", "x17", "x18", "x19", "x20", "x21", "x22", "x23",
		"x24", "x25", "x26", "x27", "x28", "x29", "x30", "x31",
	}
	
	// 也可以使用ABI别名
	riscvABI := []string{
		"zero", "ra", "sp", "gp", "tp", "t0", "t1", "t2",
		"s0",   "s1", "a0", "a1", "a2", "a3", "a4", "a5",
		"a6",   "a7", "s2", "s3", "s4", "s5", "s6", "s7",
		"s8",   "s9", "s10","s11","t3", "t4", "t5", "t6",
	}
	
	if regNum >= 0 && regNum < len(riscvRegs) {
		// 返回更易读的ABI名称
		return riscvABI[regNum]
	}
	
	// 回退到通用名称
	return fmt.Sprintf("reg%d", regNum)
}

// 生成统一的BPF代码（包含基础断点+变量监控）
func generateBPFWithVariables(ctx *DebuggerContext, requestedVars []string) error {
	// 基本检查
	if ctx == nil {
		return fmt.Errorf("Debug context is null")
	}
	if ctx.Project == nil {
		return fmt.Errorf("Project not opened")
	}
	if len(ctx.Project.Breakpoints) == 0 {
		return fmt.Errorf("No breakpoints set, current count: %d", len(ctx.Project.Breakpoints))
	}
	
	// 创建BPF文件
	bpfPath := filepath.Join(ctx.Project.RootPath, "debug_variables.bpf.c")
	file, err := os.Create(bpfPath)
	if err != nil {
		return fmt.Errorf("创建BPF文件失败: %v", err)
	}
	defer file.Close()
	
	// 检测当前架构并生成对应的定义
	currentArch := detectCurrentArch()
	archDefine, exists := SupportedArchitectures[currentArch]
	if !exists {
		archDefine = "__TARGET_ARCH_x86" // 默认架构
	}

	// 写入BPF代码头部
	fmt.Fprintln(file, "#include <linux/bpf.h>")
	fmt.Fprintln(file, "#include <bpf/bpf_helpers.h>")
	fmt.Fprintln(file, "#include <bpf/bpf_tracing.h>")
	fmt.Fprintln(file, "#include <linux/ptrace.h>")
	fmt.Fprintln(file, "#include <linux/types.h>")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "// 定义目标架构 - 解决PT_REGS_PARM错误")
	fmt.Fprintf(file, "#define %s\n", archDefine)
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "// 统一BPF调试程序（基础断点 + 变量监控）")
	fmt.Fprintln(file, "// 生成时间:", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintln(file, "")
	
	// 添加类型定义（兼容性处理）
	fmt.Fprintln(file, "// 类型定义（确保兼容性）")
	fmt.Fprintln(file, "#ifndef u32")
	fmt.Fprintln(file, "typedef __u32 u32;")
	fmt.Fprintln(file, "#endif")
	fmt.Fprintln(file, "#ifndef u64")
	fmt.Fprintln(file, "typedef __u64 u64;")
	fmt.Fprintln(file, "#endif")
	fmt.Fprintln(file, "#ifndef s64")
	fmt.Fprintln(file, "typedef __s64 s64;")
	fmt.Fprintln(file, "#endif")
	fmt.Fprintln(file, "")
	
	// 统一的调试事件结构（包含基础断点+变量信息）
	fmt.Fprintln(file, "// 统一调试事件结构")
	fmt.Fprintln(file, "struct debug_event {")
	fmt.Fprintln(file, "    // 基础断点信息")
	fmt.Fprintln(file, "    u32 pid;")
	fmt.Fprintln(file, "    u32 tgid;")
	fmt.Fprintln(file, "    u64 timestamp;")
	fmt.Fprintln(file, "    u32 breakpoint_id;")
	fmt.Fprintln(file, "    char comm[16];")
	fmt.Fprintln(file, "    char function[64];")
	if len(requestedVars) > 0 {
		fmt.Fprintln(file, "    // 变量监控信息")
		fmt.Fprintln(file, "    char var_name[32];")
		fmt.Fprintln(file, "    s64 var_value;")
		fmt.Fprintln(file, "    u8 var_type;  // 1=int, 2=long, 3=pointer")
	}
	fmt.Fprintln(file, "};")
	fmt.Fprintln(file, "")
	
	validBreakpoints := 0
	for _, bp := range ctx.Project.Breakpoints {
		if !bp.Enabled {
			continue
		}
		
		funcName := bp.Function
		if funcName == "unknown" || funcName == "" {
			// 尝试重新解析函数名
			if parsedName := parseFunctionName(bp.File, bp.Line); parsedName != "" {
				funcName = parsedName
			} else {
				continue
			}
		}
		
		fileName := filepath.Base(bp.File)
		
		// 基础断点信息
		fmt.Fprintf(file, "// 断点 %d: %s:%d 在函数 %s\n", validBreakpoints+1, fileName, bp.Line, funcName)
		fmt.Fprintf(file, "// 功能: 基础断点监控")
		
		// 如果有变量请求，获取变量位置信息
		var varLocations map[string]VariableLocation
		if len(requestedVars) > 0 {
			varLocations = parseDWARFVariableLocations(bp.File, bp.Line, requestedVars)
			if len(varLocations) > 0 {
				fmt.Fprintf(file, " + 变量监控")
				fmt.Fprintf(file, " (")
				first := true
				for varName := range varLocations {
					if !first {
						fmt.Fprintf(file, ", ")
					}
					fmt.Fprintf(file, "%s", varName)
					first = false
				}
				fmt.Fprintf(file, ")")
			}
		}
		fmt.Fprintln(file)
		
		fmt.Fprintf(file, "SEC(\"kprobe/%s\")\n", funcName)
		fmt.Fprintf(file, "int trace_debug_%d(struct pt_regs *ctx) {\n", validBreakpoints)
		fmt.Fprintln(file, "    struct debug_event event = {};")
		fmt.Fprintln(file, "")
		fmt.Fprintln(file, "    // 基础断点信息收集")
		fmt.Fprintln(file, "    u64 pid_tgid = bpf_get_current_pid_tgid();")
		fmt.Fprintln(file, "    event.pid = pid_tgid;")
		fmt.Fprintln(file, "    event.tgid = pid_tgid >> 32;")
		fmt.Fprintln(file, "    event.timestamp = bpf_ktime_get_ns();")
		fmt.Fprintf(file, "    event.breakpoint_id = %d;\n", validBreakpoints)
		fmt.Fprintln(file, "    bpf_get_current_comm(&event.comm, sizeof(event.comm));")
		fmt.Fprintf(file, "    bpf_probe_read_str(&event.function, sizeof(event.function), \"%s\");\n", funcName)
		fmt.Fprintln(file, "")
		
		// 基础断点输出
		fmt.Fprintf(file, "    // 基础断点输出\n")
		fmt.Fprintf(file, "    bpf_printk(\"[BREAKPOINT-%d] %s:%d in %%s() PID=%%d TGID=%%d at %%llu\\n\", \n", 
			validBreakpoints+1, fileName, bp.Line)
		fmt.Fprintln(file, "               event.function, event.pid, event.tgid, event.timestamp);")
		fmt.Fprintln(file, "")
		
		// 如果有变量，生成变量读取代码
		if len(varLocations) > 0 {
			fmt.Fprintln(file, "    // 变量监控（如果有请求的变量）")
			for varName, location := range varLocations {
				fmt.Fprintf(file, "    // 读取变量: %s\n", varName)
				fmt.Fprintf(file, "    bpf_probe_read_str(&event.var_name, sizeof(event.var_name), \"%s\");\n", varName)
				
				switch location.Type {
				case "register":
					fmt.Fprintf(file, "    event.var_value = PT_REGS_%s(ctx);\n", strings.ToUpper(location.Register))
				case "stack":
					fmt.Fprintln(file, "    {")
					fmt.Fprintf(file, "        void *stack_addr = (void *)(PT_REGS_FP(ctx) + %d);\n", location.StackOffset)
					fmt.Fprintln(file, "        long temp_val = 0;")
					fmt.Fprintf(file, "        if (bpf_probe_read_user(&temp_val, %d, stack_addr) == 0) {\n", location.Size)
					fmt.Fprintln(file, "            event.var_value = temp_val;")
					fmt.Fprintln(file, "        }")
					fmt.Fprintln(file, "    }")
				case "memory":
					fmt.Fprintln(file, "    // Memory variable access not implemented yet")
				}
				
				fmt.Fprintln(file, "    event.var_type = 2;  // long type")
				fmt.Fprintf(file, "    bpf_printk(\"[VAR-%d] %s:%%s=%%ld PID=%%d\\n\", event.var_name, event.var_value, event.pid);\n", 
					validBreakpoints+1, funcName)
				fmt.Fprintln(file, "")
			}
		}
		
		fmt.Fprintln(file, "    return 0;")
		fmt.Fprintln(file, "}")
		fmt.Fprintln(file, "")
		
		validBreakpoints++
	}
	
	if validBreakpoints == 0 {
		return fmt.Errorf("没有找到有效的函数名，无法生成BPF探针")
	}
	
	fmt.Fprintln(file, "char LICENSE[] SEC(\"license\") = \"GPL\";")
	
	return nil
}

// 编译BPF代码（带架构参数）
func compileBPFWithArch(ctx *DebuggerContext, targetArch string) error {
	if ctx.Project == nil {
		return fmt.Errorf("没有打开的项目")
	}
	
	// 检查BPF源文件是否存在
	bpfSourcePath := filepath.Join(ctx.Project.RootPath, "debug_breakpoints.bpf.c")
	if _, err := os.Stat(bpfSourcePath); os.IsNotExist(err) {
		return fmt.Errorf("BPF源文件不存在: %s\n请先使用 'generate' 命令生成BPF代码", bpfSourcePath)
	}
	
	// 目标文件路径
	bpfObjectPath := filepath.Join(ctx.Project.RootPath, "debug_breakpoints.bpf.o")
	
	// 检查clang编译器是否可用
	if _, err := exec.LookPath("clang"); err != nil {
		return fmt.Errorf("找不到clang编译器，请安装:\n  Ubuntu/Debian: sudo apt install clang\n  CentOS/RHEL: sudo yum install clang")
	}
	
	// 获取架构对应的BPF定义
	archDefine, exists := SupportedArchitectures[targetArch]
	if !exists {
		return fmt.Errorf("不支持的架构: %s", targetArch)
	}

	// 构建编译命令
	compileCmd := exec.Command("clang", 
		"-target", "bpf",
		"-O2",
		"-g",
		fmt.Sprintf("-D%s=1", archDefine),
		"-c", bpfSourcePath,
		"-o", bpfObjectPath)
	
	// 设置工作目录
	compileCmd.Dir = ctx.Project.RootPath
	
	// 执行编译
	output, err := compileCmd.CombinedOutput()
	if err != nil {
		// 编译失败，返回详细错误信息
		return fmt.Errorf("BPF编译失败:\n编译命令: %s\n错误输出:\n%s\n\n常见问题排查:\n• 检查是否安装了linux-headers\n• 确认clang版本支持BPF目标\n• 验证BPF源代码语法", 
			compileCmd.String(), string(output))
	}
	
	// 检查输出文件是否生成
	if _, err := os.Stat(bpfObjectPath); os.IsNotExist(err) {
		return fmt.Errorf("编译完成但未找到输出文件: %s", bpfObjectPath)
	}
	
	return nil
}

// 编译BPF代码（旧版本，保持向后兼容）
func compileBPF(ctx *DebuggerContext) error {
	currentArch := detectCurrentArch()
	return compileBPFWithArch(ctx, currentArch)
}

// 编译变量监控BPF代码
// 编译变量监控BPF代码（带架构参数）
func compileVariableBPFWithArch(ctx *DebuggerContext, targetArch string) error {
	if ctx.Project == nil {
		return fmt.Errorf("没有打开的项目")
	}
	
	// 检查BPF源文件是否存在
	bpfSourcePath := filepath.Join(ctx.Project.RootPath, "debug_variables.bpf.c")
	if _, err := os.Stat(bpfSourcePath); os.IsNotExist(err) {
		return fmt.Errorf("变量监控BPF源文件不存在: %s\n请先使用 'vars <variable_names>' 命令生成代码", bpfSourcePath)
	}
	
	// 目标文件路径
	bpfObjectPath := filepath.Join(ctx.Project.RootPath, "debug_variables.bpf.o")
	
	// 检查clang编译器是否可用
	if _, err := exec.LookPath("clang"); err != nil {
		return fmt.Errorf("找不到clang编译器，请安装:\n  Ubuntu/Debian: sudo apt install clang\n  CentOS/RHEL: sudo yum install clang")
	}
	
	// 获取架构对应的BPF定义
	archDefine, exists := SupportedArchitectures[targetArch]
	if !exists {
		return fmt.Errorf("不支持的架构: %s", targetArch)
	}

	// 构建编译命令
	compileCmd := exec.Command("clang", 
		"-target", "bpf",
		"-O2",
		"-g",
		fmt.Sprintf("-D%s=1", archDefine),
		"-c", bpfSourcePath,
		"-o", bpfObjectPath)
	
	// 设置工作目录
	compileCmd.Dir = ctx.Project.RootPath
	
	// 执行编译
	output, err := compileCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("变量监控BPF编译失败:\n编译命令: %s\n错误输出:\n%s\n\n常见问题排查:\n• 检查是否安装了linux-headers\n• 确认clang版本支持BPF目标\n• 验证变量监控BPF源代码语法", 
			compileCmd.String(), string(output))
	}
	
	// 检查输出文件是否生成
	if _, err := os.Stat(bpfObjectPath); os.IsNotExist(err) {
		return fmt.Errorf("编译完成但未找到输出文件: %s", bpfObjectPath)
	}
	
	return nil
}

// 编译变量监控BPF代码（旧版本，保持向后兼容）
func compileVariableBPF(ctx *DebuggerContext) error {
	currentArch := detectCurrentArch()
	return compileVariableBPFWithArch(ctx, currentArch)
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
		debugMsg := fmt.Sprintf("[ERROR] Failed to close popup window with q key: %s, error: %v", popupID, err)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, debugMsg)
	} else {
		debugMsg := fmt.Sprintf("[DEBUG] Successfully closed popup window with q key: %s", popupID)
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
		fmt.Fprintf(v, "\x1b[90mPress q to close | Drag title bar to move window\x1b[0m\n")
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
			fmt.Fprintf(v, "\x1b[90m[%d/%d] Use ↑↓ to scroll\x1b[0m", popup.ScrollY+1, len(popup.Content)-availableLines+1)
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
	stateStr := "STOP"
	if ctx.BpfLoaded {
		stateStr = "BPF_LOADED"
	}
	if ctx.Running {
		stateStr = "RUNNING"
	}
	
	// 显示基本状态信息
	fmt.Fprintf(v, "RISC-V Kernel Debugger | State: %s | Func: %s | Addr: 0x%X", 
		stateStr, ctx.CurrentFunc, ctx.CurrentAddr)
	
	// 显示全屏状态和操作提示
	if ctx.IsFullscreen {
		fmt.Fprintf(v, " | Fullscreen: %s | F11/ESC-Exit", ctx.FullscreenView)
	} else {
		// 显示拖拽状态和提示
		if ctx.Layout != nil {
			if ctx.Layout.IsDragging {
				fmt.Fprintf(v, " | Resizing: %s", getBoundaryName(ctx.Layout.DragBoundary))
			} else {
				fmt.Fprint(v, " | Tip: Drag borders to resize, F11 for fullscreen")
			}
			
			// 显示当前布局参数
			fmt.Fprintf(v, " | Layout: L%d R%d C%d", 
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

// ========== 文件浏览器窗口内容刷新 ==========
func updateFileBrowserView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("filebrowser")
	if err != nil {
		return
	}
	v.Clear()
	
	if g.CurrentView() != nil && g.CurrentView().Name() == "filebrowser" {
		fmt.Fprintln(v, "\x1b[43;30m▶ File Browser (Focused)\x1b[0m")
	} else {
		fmt.Fprintln(v, "File Browser")
	}
	
	if ctx.Project == nil {
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "No project opened")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "Use command to open project:")
		fmt.Fprintln(v, "open /path/to/project")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "Or:")
		fmt.Fprintln(v, "open ../tacosys_ko")
		return
	}
	
	fmt.Fprintln(v, "")
	fmt.Fprintf(v, "Project: %s\n", filepath.Base(ctx.Project.RootPath))
	fmt.Fprintln(v, "💡 Click file to open, click folder to expand/collapse")
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
		fmt.Fprintln(v, "\x1b[43;30m▶ Registers (Focused)\x1b[0m")
	} else {
		fmt.Fprintln(v, "Registers")
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
		fmt.Fprintln(v, "\x1b[43;30m▶ Variables (Focused)\x1b[0m")
	} else {
		fmt.Fprintln(v, "Variables")
	}
	lines := []string{
		"Local variables:",
		"ctx      debugger_ctx_t* 0x7fff1234",
		"fd       int             3",
		"ret      int            -1",
		"...",
		"", "Global variables:",
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
		fmt.Fprintln(v, "\x1b[43;30m▶ Call Stack (Focused)\x1b[0m")
	} else {
		fmt.Fprintln(v, "Call Stack")
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
	
	// 显示标题行，包含搜索状态
	if g.CurrentView() != nil && g.CurrentView().Name() == "code" {
		if ctx.SearchMode {
			searchStatus := ""
			if len(ctx.SearchResults) > 0 {
				searchStatus = fmt.Sprintf(" | Search: \"%s\" (%d/%d)", 
					ctx.SearchTerm, ctx.CurrentMatch+1, len(ctx.SearchResults))
			} else if ctx.SearchTerm != "" {
				searchStatus = fmt.Sprintf(" | Search: \"%s\" (no results)", ctx.SearchTerm)
			} else {
				searchStatus = fmt.Sprintf(" | Search: \"%s\"", ctx.SearchInput)
			}
			fmt.Fprintf(v, "\x1b[43;30m▶ Code View (Focused) %s\x1b[0m\n", searchStatus)
		} else {
			fmt.Fprintln(v, "\x1b[43;30m▶ Code View (Focused)\x1b[0m")
		}
	} else {
		if ctx.SearchMode {
			searchStatus := ""
			if len(ctx.SearchResults) > 0 {
				searchStatus = fmt.Sprintf(" | Search: \"%s\" (%d/%d)", 
					ctx.SearchTerm, ctx.CurrentMatch+1, len(ctx.SearchResults))
			} else if ctx.SearchTerm != "" {
				searchStatus = fmt.Sprintf(" | Search: \"%s\" (no results)", ctx.SearchTerm)
			} else {
				searchStatus = fmt.Sprintf(" | Search: \"%s\"", ctx.SearchInput)
			}
			fmt.Fprintf(v, "Code View%s\n", searchStatus)
		} else {
			fmt.Fprintln(v, "Code View")
		}
	}
	
	// 如果有打开的文件，显示文件内容
	if ctx.Project != nil && ctx.Project.CurrentFile != "" {
		lines, exists := ctx.Project.OpenFiles[ctx.Project.CurrentFile]
		if !exists {
			// 尝试读取文件
			var err error
			lines, err = readFileContent(ctx.Project.CurrentFile)
			if err != nil {
				fmt.Fprintf(v, "Cannot read file: %v\n", err)
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
			
			// 应用搜索高亮
			highlightedLine := highlightSearchMatches(line, lineNum, ctx)
			
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

// ========== 断点窗口内容刷新 ==========
func updateBreakpointsView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("stack")
	if err != nil {
		return
	}
	v.Clear()
	
	if g.CurrentView() != nil && g.CurrentView().Name() == "stack" {
		fmt.Fprintln(v, "\x1b[43;30m▶ Breakpoint Manager (Focused)\x1b[0m")
	} else {
		fmt.Fprintln(v, "Breakpoint Manager")
	}
	
	if ctx.Project == nil {
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "No project opened")
		return
	}
	
	fmt.Fprintln(v, "")
	if len(ctx.Project.Breakpoints) == 0 {
		fmt.Fprintln(v, "No breakpoints")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "Press Enter in code view to set breakpoint")
	} else {
		fmt.Fprintf(v, "Breakpoint List (%d):\n", len(ctx.Project.Breakpoints))
		fmt.Fprintln(v, "")
		
		for i, bp := range ctx.Project.Breakpoints {
			status := "✓"
			if !bp.Enabled {
				status = "✗"
			}
			
			fileName := filepath.Base(bp.File)
			fmt.Fprintf(v, "%d. %s %s:%d\n", i+1, status, fileName, bp.Line)
			if bp.Function != "unknown" {
				fmt.Fprintf(v, "   Function: %s\n", bp.Function)
			}
		}
		
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "g-Generate BPF  c-Clear all breakpoints")
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
	
	return fmt.Errorf("Cannot access clipboard, please install xclip or xsel")
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
		debugInfo := fmt.Sprintf("[DEBUG] Path command length=%d: %s", len(command), command)
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
			"🎯 Kernel Debugger - Command Reference",
			"",
			"🚀 Quick Start:",
			"  1. open /path/to/project    - Open project",
			"  2. Double-click code lines  - Set breakpoints", 
			"  3. vars                    - Auto-generate debug code",
			"  4. compile                 - Build BPF program",
			"",
			"📂 Project Commands:",
			"  open <path>    - Open project directory",
			"  close          - Close current project",
			"  pwd            - Show current directory",
			"  status         - Show debugger status",
			"",
			"🔴 Breakpoint Commands:",
			"  bp             - View all breakpoints",
			"  bp clear       - Clear all breakpoints",
			"  (Interactive)  - Double-click code line to set/toggle breakpoint",
			"",
			"🤖 Debug Code Generation:",
			"  vars           - 🔥 Auto-detect all variables + generate BPF",
			"  vars auto      - Same as above (explicit auto mode)",
			"  vars <names>   - Manual variable specification (e.g. vars local_var i)",
			"  compile        - 🏗️ Auto-detect current architecture and compile",
			"  compile <arch> - Compile for specific architecture (x86/arm64/riscv64/etc)",
			"  generate       - Basic function monitoring only (legacy)",
			"",
			"⌨️ Interface:",
			"  help, h        - Show this help",
			"  clear          - Clear command output",
			"  Ctrl+F         - Search in code",
			"  F3             - Next search result",
			"  Tab            - Switch windows",
			"  F1-F6          - Direct window switch (Files/Registers/Variables/Stack/Code/Command)",
			"  F11            - Toggle fullscreen",
			"  ESC            - Exit fullscreen/search",
			"  q              - Close popup windows",
			"",
			"🏗️ Architecture Support:",
			"  ✅ x86_64 (Intel/AMD 64-bit)",
			"  ✅ ARM64/AArch64",
			"  ✅ RISC-V 64-bit",
			"  ✅ IBM System z (s390x)",
			"  ✅ PowerPC 64-bit LE",
			"  ✅ MIPS 64-bit",
			"  📋 Interactive selection during compile",
			"",
			"🔍 BPF Monitoring Capabilities:",
			"  ✅ Function call detection",
			"  ✅ Process info (PID, name)",
			"  ✅ Precise timestamps",
			"  ✅ Variable values (with vars)",
			"  ⚠️  Limitation: Function entry only, not specific lines",
			"",
			"📁 Generated Files (vars command):",
			"  • debug_variables.bpf.c",
			"  • load_debug_vars.sh",
			"  • unload_debug_vars.sh",
			"",
			"🔄 Typical Workflow:",
			"  open . → Double-click lines → vars → compile → exit",
			"  sudo ./load_debug_vars.sh",
			"  sudo cat /sys/kernel/debug/tracing/trace_pipe",
			"  sudo ./unload_debug_vars.sh",
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
			output = []string{"Error: Usage: open <project_path>", "Tip: Supports paths with spaces, e.g.: open /path/to/folder with spaces"}
		} else {
			projectPath := args  // 直接使用args，保留所有空格
			output = append(output, fmt.Sprintf("Processing path: %s", projectPath))
			
			// 如果是相对路径，转换为绝对路径
			if !filepath.IsAbs(projectPath) {
				wd, _ := os.Getwd()
				projectPath = filepath.Join(wd, projectPath)
				output = append(output, fmt.Sprintf("Converting to absolute path: %s", projectPath))
			}
			
			// 检查路径是否存在
			if _, err := os.Stat(projectPath); os.IsNotExist(err) {
				output = []string{fmt.Sprintf("Error: Path does not exist: %s", projectPath)}
			} else {
				output = append(output, "Path exists, opening project...")
				
				project, err := openProject(projectPath)
				if err != nil {
					output = append(output, fmt.Sprintf("Error: Failed to open project: %v", err))
				} else {
					globalCtx.Project = project
					fileCount := countFiles(project.FileTree)
					output = append(output, []string{
						fmt.Sprintf("Successfully opened project: %s", filepath.Base(projectPath)),
						fmt.Sprintf("Found %d files", fileCount),
						"Use F1 to switch to file browser to view file tree",
					}...)
				}
			}
		}
		
	case "generate", "g":
		if globalCtx.Project == nil {
			output = []string{"Error: Please open a project first"}
		} else {
			err := generateBPF(globalCtx)
			if err != nil {
				output = []string{fmt.Sprintf("Error: Failed to generate BPF: %v", err)}
			} else {
				output = []string{
					"Success: Generated LEGACY BPF debug code",
					"Files created:",
					"  • debug_breakpoints.bpf.c (BPF program)",
					"  • load_debug_bpf.sh (loading script)",
					"  • unload_debug_bpf.sh (cleanup script)",
					"",
					"⚠️  Legacy Command Notice:",
					"• This command generates OLD-STYLE basic breakpoint monitoring only",
					"• For MODERN unified debugging, use 'vars' command instead:",
					"  - vars               → basic function monitoring (recommended)",
					"  - vars var1 var2     → function + variable monitoring",
					"",
					"🔄 Migration suggestion:",
					"• Use 'vars' for future debugging sessions",
					"• Current 'generate' output provides function-level monitoring only",
					"",
					"⚡ What this BPF program monitors:",
					"✅ Function call detection (when functions are invoked)",
					"✅ Process information (PID, TGID, process name)",
					"✅ Precise timestamps (nanosecond precision)",
					"❌ NO variable monitoring (use 'vars' for variables)",
					"",
					"Next steps:",
					"1. Use 'compile' command to build BPF program",
					"2. Exit TUI and run: sudo ./load_debug_bpf.sh",
					"3. View output: sudo cat /sys/kernel/debug/tracing/trace_pipe",
					"4. Cleanup: sudo ./unload_debug_bpf.sh",
				}
				globalCtx.BpfLoaded = true
			}
		}
		
	case "vars":
		if globalCtx.Project == nil {
			output = []string{
				"Error: Please open a project first",
				"Use 'open <project_path>' to open a project",
				"Example: open /tmp/test_project",
			}
		} else {
			// 添加状态诊断信息
			output = []string{
				fmt.Sprintf("Project Status: %s", filepath.Base(globalCtx.Project.RootPath)),
				fmt.Sprintf("Breakpoints Count: %d", len(globalCtx.Project.Breakpoints)),
			}
			
			// 如果断点为空，尝试重新加载
			if len(globalCtx.Project.Breakpoints) == 0 {
				output = append(output, "No breakpoints in memory, attempting to reload...")
				
				// 手动重新加载断点
				if err := loadBreakpoints(globalCtx); err != nil {
					output = append(output, fmt.Sprintf("Reload failed: %v", err))
				} else {
					output = append(output, fmt.Sprintf("Reload successful, found %d breakpoints", len(globalCtx.Project.Breakpoints)))
				}
			}
			
			// 显示断点信息
			if len(globalCtx.Project.Breakpoints) > 0 {
				output = append(output, "Current Breakpoints:")
				for i, bp := range globalCtx.Project.Breakpoints {
					output = append(output, fmt.Sprintf("  %d. %s:%d (%s) enabled=%t", 
						i+1, filepath.Base(bp.File), bp.Line, bp.Function, bp.Enabled))
				}
				output = append(output, "")
			}
			
			// 解析变量名列表（增强功能：自动检测）
			var varNames []string
			autoDetected := false
			
			if args == "" || args == "auto" {
				// 自动检测模式：扫描所有断点的函数变量
				autoDetected = true
				allVarsSet := make(map[string]bool)
				
				for _, bp := range globalCtx.Project.Breakpoints {
					if bp.Enabled {
						if detectedVars := parseAllFunctionVariables(bp.File, bp.Line); len(detectedVars) > 0 {
							for _, v := range detectedVars {
								allVarsSet[v] = true
							}
						}
					}
				}
				
				// 转换为slice
				for v := range allVarsSet {
					varNames = append(varNames, v)
				}
				
				if len(varNames) > 0 {
					output = append(output, fmt.Sprintf("🔍 Auto-detected %d variables from all breakpoint functions:", len(varNames)))
					output = append(output, fmt.Sprintf("Variables: %v", varNames))
					output = append(output, "")
				} else {
					output = append(output, "⚠️ No variables auto-detected, using common patterns")
				}
			} else {
				// 手动指定变量模式
				varNames = strings.Fields(args)
				output = append(output, fmt.Sprintf("🎯 Manual variable specification: %v", varNames))
				output = append(output, "")
			}
			
			// 生成统一的BPF程序
			err := generateBPFWithVariables(globalCtx, varNames)
			if err != nil {
				output = append(output, fmt.Sprintf("Error: Failed to generate BPF: %v", err))
			} else {
				// 生成加载和卸载脚本
				scriptPath := filepath.Join(globalCtx.Project.RootPath, "load_debug_vars.sh")
				generateVarsLoadScript(scriptPath, len(globalCtx.Project.Breakpoints))
				
				unloadScriptPath := filepath.Join(globalCtx.Project.RootPath, "unload_debug_vars.sh")
				generateVarsUnloadScript(unloadScriptPath)
				
				if len(varNames) > 0 {
					// 有变量的情况
					modeDesc := "manual specification"
					if autoDetected {
						modeDesc = "auto-detection"
					}
					
					output = append(output, []string{
						fmt.Sprintf("Success: Generated UNIFIED BPF debugging (breakpoints + variables via %s)", modeDesc),
						fmt.Sprintf("Monitoring %d variables: %v", len(varNames), varNames),
						"",
						"🔥 What this BPF program monitors:",
						"✅ Function call detection (when functions are invoked)",
						"✅ Process information (PID, TGID, process name)",
						"✅ Precise timestamps (nanosecond precision)",
						"✅ Variable value monitoring (real-time tracking)",
						"✅ Register and stack variable support",
						"",
						"⚠️  Important understanding:",
						"• BPF sets probes at FUNCTION ENTRY, not specific code lines",
						"• Can detect IF a function runs, but NOT which lines inside execute",
						"• This is a BPF/kprobe technical limitation",
						"• Line numbers in output show where you set breakpoints for reference",
					}...)
					
					if autoDetected {
						output = append(output, []string{
							"",
							"🤖 Auto-Detection Features:",
							"• Automatically scanned all functions with breakpoints",
							"• Parsed source code for local variable declarations",
							"• Extracted variable names using pattern matching",
							"• Next: Use 'vars var1 var2' to manually specify variables",
						}...)
					}
				} else {
					// 仅基础断点的情况
					output = append(output, []string{
						"Success: Generated BASIC BPF debugging (function monitoring only)",
						"",
						"🔥 What this BPF program monitors:",
						"✅ Function call detection (when functions are invoked)",
						"✅ Process information (PID, TGID, process name)",
						"✅ Precise timestamps (nanosecond precision)",
						"",
						"⚠️  Important understanding:",
						"• BPF sets probes at FUNCTION ENTRY, not specific code lines",
						"• Can detect IF a function runs, but NOT which lines inside execute",
						"• To monitor variables, use: vars var1 var2 var3",
						"",
						"💡 Usage examples:",
						"• vars                    → auto-detect all function variables",
						"• vars auto               → same as above",
						"• vars local_var counter  → manual variable specification",
					}...)
				}
				
				output = append(output, []string{
					"",
					"📁 Files created:",
					"  • debug_variables.bpf.c (unified BPF program)",
					"  • load_debug_vars.sh (loading script)",  
					"  • unload_debug_vars.sh (cleanup script)",
					"",
					"⚡ Quick Start:",
					"1. Use 'compile' command to build BPF program",
					"2. Exit TUI and run: sudo ./load_debug_vars.sh",
					"3. View output: sudo cat /sys/kernel/debug/tracing/trace_pipe",
					"4. Cleanup: sudo ./unload_debug_vars.sh",
				}...)
			}
		}
		
	case "compile":
		if globalCtx.Project == nil {
			output = []string{"Error: Please open a project first"}
		} else {
			// 解析架构参数
			var targetArch string
			if args == "" {
				// 没有指定架构，使用当前系统架构
				targetArch = detectCurrentArch()
				output = []string{
					"🏗️ Architecture Selection",
					fmt.Sprintf("Auto-detected: %s (%s)", targetArch, ArchDisplayNames[targetArch]),
					"",
					"💡 Available architectures:",
					"  compile x86     - Intel/AMD 64-bit",
					"  compile arm64   - ARM 64-bit", 
					"  compile riscv64 - RISC-V 64-bit",
					"  compile s390x   - IBM System z",
					"  compile ppc64le - PowerPC 64-bit LE",
					"  compile mips64  - MIPS 64-bit",
					"",
					"  compile         - Use current system arch (auto-detect)",
					"",
					fmt.Sprintf("✅ Using current system architecture: %s", targetArch),
				}
			} else {
				// 用户指定了架构
				switch strings.ToLower(args) {
				case "x86", "x86_64":
					targetArch = "x86_64"
				case "arm64", "aarch64":
					targetArch = "aarch64"
				case "riscv64", "riscv":
					targetArch = "riscv64"
				case "s390x":
					targetArch = "s390x"
				case "ppc64le", "powerpc":
					targetArch = "ppc64le"
				case "mips64":
					targetArch = "mips64"
				default:
					output = []string{
						fmt.Sprintf("Error: Unsupported architecture '%s'", args),
						"",
						"Supported architectures:",
						"  x86, x86_64     - Intel/AMD 64-bit",
						"  arm64, aarch64  - ARM 64-bit",
						"  riscv64, riscv  - RISC-V 64-bit", 
						"  s390x           - IBM System z",
						"  ppc64le         - PowerPC 64-bit LE",
						"  mips64          - MIPS 64-bit",
						"",
						"Examples:",
						"  compile         - Auto-detect current system",
						"  compile x86     - Target x86_64",
						"  compile arm64   - Target ARM64",
					}
					break
				}
				output = []string{
					"🏗️ Architecture Selection",
					fmt.Sprintf("User specified: %s (%s)", targetArch, ArchDisplayNames[targetArch]),
					"",
					fmt.Sprintf("✅ Using target architecture: %s", targetArch),
				}
			}
			
			// 智能检测编译哪种BPF文件
			varsFile := filepath.Join(globalCtx.Project.RootPath, "debug_variables.bpf.c")
			breakpointsFile := filepath.Join(globalCtx.Project.RootPath, "debug_breakpoints.bpf.c")
			
			var err error
			var compiledFile string
			var scriptFile string
			
			// 优先编译变量监控版本（如果存在）
			if _, varsErr := os.Stat(varsFile); varsErr == nil {
				err = compileVariableBPFWithArch(globalCtx, targetArch)
				compiledFile = "debug_variables.bpf.o"
				scriptFile = "./load_debug_vars.sh"
			} else if _, bpErr := os.Stat(breakpointsFile); bpErr == nil {
				err = compileBPFWithArch(globalCtx, targetArch)
				compiledFile = "debug_breakpoints.bpf.o"
				scriptFile = "./load_debug_bpf.sh"
			} else {
				output = append(output, []string{
					"",
					"Error: No BPF source files found",
					"",
					"Please generate BPF code first:",
					"• Use 'vars' for modern unified debugging (recommended)",
					"• Use 'vars var1 var2' for debugging with variable monitoring",
					"• Use 'generate' for legacy basic breakpoint debugging only",
				}...)
				break
			}
			
			if err != nil {
				output = append(output, []string{
					"",
					fmt.Sprintf("❌ Compilation failed: %v", err),
					"",
					"💡 Troubleshooting:",
					"• Check if clang supports BPF: clang -target bpf --help",
					"• Install headers: sudo apt install linux-headers-$(uname -r)",
					"• Try different architecture: compile <arch>",
				}...)
			} else {
				output = append(output, []string{
					"",
					"✅ BPF code compilation completed successfully!",
					fmt.Sprintf("📁 Output file: %s", compiledFile),
					fmt.Sprintf("🏗️ Target arch: %s (%s)", targetArch, ArchDisplayNames[targetArch]),
					"",
					"🔥 BPF Compilation Details:",
					"• Uses clang BPF backend for optimal code generation",
					"• Architecture-specific PT_REGS macro selection",
					"• O2 optimization level for BPF verifier compatibility",
					"• Cross-platform bytecode generation",
					"",
					fmt.Sprintf("⚡ Next step: sudo %s", scriptFile),
					"📊 Monitor: sudo cat /sys/kernel/debug/tracing/trace_pipe",
				}...)
			}
		}
		

	case "bp":
		if args == "clear" {
			// bp clear - 清除所有断点
			if globalCtx.Project != nil {
				count := len(globalCtx.Project.Breakpoints)
				globalCtx.Project.Breakpoints = make([]Breakpoint, 0)
				// 保存清空后的断点列表
				if err := saveBreakpoints(globalCtx); err != nil {
					output = []string{fmt.Sprintf("Warning: Breakpoints cleared but save failed: %v", err)}
				} else {
					output = []string{fmt.Sprintf("Success: Cleared %d breakpoints", count)}
				}
			} else {
				output = []string{"Tip: No project opened"}
			}
		} else {
			// bp - 查看断点（默认行为）
			if globalCtx.Project == nil {
				output = []string{"Error: Please open a project first"}
			} else {
				// 创建断点查看弹出窗口
				showBreakpointsPopup(globalCtx)
				output = []string{"Breakpoint viewer window opened"}
			}
		}
		
	case "close":
		if globalCtx.Project != nil {
			projectName := filepath.Base(globalCtx.Project.RootPath)
			globalCtx.Project = nil
			output = []string{fmt.Sprintf("Success: Closed project %s", projectName)}
		} else {
			output = []string{"Tip: No project opened"}
		}
		

	case "status":
		output = []string{
			fmt.Sprintf("Debugger status: %s", globalCtx.CurrentFunc),
			fmt.Sprintf("Current address: 0x%X", globalCtx.CurrentAddr),
		}
		if globalCtx.Project != nil {
			output = append(output, fmt.Sprintf("Project: %s", filepath.Base(globalCtx.Project.RootPath)))
			output = append(output, fmt.Sprintf("Breakpoints: %d", len(globalCtx.Project.Breakpoints)))
		} else {
			output = append(output, "Project: Not opened")
		}
		
	default:
		output = []string{
			fmt.Sprintf("bash: %s: command not found", cmd),
			"Type 'help' to see available commands",
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
			"No breakpoints set currently",
			"",
			"Usage:",
			"• Double-click code line in code view to set breakpoint",
			"• Press Enter key also sets breakpoint",
			"• Click same line again to toggle breakpoint enable/disable status",
		}
	} else {
		content = append(content, fmt.Sprintf("Total %d breakpoints:", len(ctx.Project.Breakpoints)))
		content = append(content, "")
		content = append(content, "Status | File | Line | Function")
		content = append(content, "------ | ---- | ---- | --------")
		
		for i, bp := range ctx.Project.Breakpoints {
			status := "✓ Enabled"
			if !bp.Enabled {
				status = "✗ Disabled"
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
		content = append(content, "Operations:")
		content = append(content, "• Breakpoints auto-saved to .debug_breakpoints.json")
		content = append(content, "• Auto-load breakpoints when reopening project")
		content = append(content, "• Use 'generate' command to create BPF debug code")
		content = append(content, "")
		content = append(content, "🔥 Close window: Press q key or click outside window border")
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
	popup := createPopupWindow(ctx, "breakpoints", "Breakpoint Viewer", width, height, content)
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
			debugInfo := fmt.Sprintf("[DEBUG] Dot input, view: %s, current input length: %d", currentViewName, len(globalCtx.CurrentInput))
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

// ========== 搜索事件处理函数 ==========

// Ctrl+F启动搜索模式
func startSearchHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil {
		return nil
	}
	
	// 只在代码视图中启动搜索
	if v != nil && v.Name() == "code" {
		if globalCtx.Project == nil || globalCtx.Project.CurrentFile == "" {
					// 在命令历史中显示提示
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, "[INFO] Please open a file first to search")
		globalCtx.CommandDirty = true
		return nil
		}
		
		startSearchMode(globalCtx)
		
		// 在命令历史中显示搜索提示
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, "[SEARCH] Search mode activated, type keywords and press Enter to search, ESC to exit")
		globalCtx.CommandDirty = true
	}
	
	return nil
}

// 搜索模式下的字符输入处理
func handleSearchCharInput(ch rune) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if globalCtx == nil || !globalCtx.SearchMode {
			return nil
		}
		
		// 只在代码视图聚焦时处理搜索输入
		if v != nil && v.Name() == "code" {
			globalCtx.SearchInput += string(ch)
		}
		
		return nil
	}
}

// 搜索模式下的退格键处理
func handleSearchBackspace(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || !globalCtx.SearchMode {
		return nil
	}
	
	// 只在代码视图聚焦时处理搜索输入
	if v != nil && v.Name() == "code" {
		if len(globalCtx.SearchInput) > 0 {
			globalCtx.SearchInput = globalCtx.SearchInput[:len(globalCtx.SearchInput)-1]
		}
	}
	
	return nil
}

// 搜索模式下的回车键处理
func handleSearchEnter(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || !globalCtx.SearchMode {
		return nil
	}
	
	// 只在代码视图聚焦时处理
	if v != nil && v.Name() == "code" {
		if globalCtx.SearchInput != "" {
			// 如果是新的搜索词，执行搜索
			if globalCtx.SearchTerm != globalCtx.SearchInput {
				globalCtx.SearchTerm = globalCtx.SearchInput
				performSearch(globalCtx)
				
				// 显示搜索结果统计
				if len(globalCtx.SearchResults) > 0 {
					globalCtx.CommandHistory = append(globalCtx.CommandHistory, 
						fmt.Sprintf("[SEARCH] Found %d matches", len(globalCtx.SearchResults)))
				} else {
					globalCtx.CommandHistory = append(globalCtx.CommandHistory, 
						fmt.Sprintf("[SEARCH] No matches found: \"%s\"", globalCtx.SearchTerm))
				}
				globalCtx.CommandDirty = true
			} else {
				// 跳转到下一个匹配项
				jumpToNextMatch(globalCtx)
			}
		}
	}
	
	return nil
}

// 搜索模式下的ESC键处理
func handleSearchEscape(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil {
		return nil
	}
	
	if globalCtx.SearchMode {
		// 退出搜索模式
		exitSearchMode(globalCtx)
		globalCtx.CommandHistory = append(globalCtx.CommandHistory, "[SEARCH] Search mode exited")
		globalCtx.CommandDirty = true
		return nil
	}
	
	// 如果不在搜索模式，调用原有的ESC处理
	return escapeExitFullscreenHandler(g, v)
}

// Shift+F3跳转到上一个匹配项
func jumpToPrevMatchHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || !globalCtx.SearchMode {
		return nil
	}
	
	jumpToPrevMatch(globalCtx)
	return nil
}

// F3跳转到下一个匹配项
func jumpToNextMatchHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || !globalCtx.SearchMode {
		return nil
	}
	
	jumpToNextMatch(globalCtx)
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
			fmt.Fprintf(cmdView, "Failed to generate BPF: %v\n", err)
		}
	} else {
		globalCtx.BpfLoaded = true
		// 在命令窗口显示成功
		if cmdView, err := g.View("command"); err == nil {
			cmdView.Clear()
			fmt.Fprintln(cmdView, "BPF code generation successful!")
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
			fmt.Fprintln(cmdView, "All breakpoints cleared")
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
				fmt.Fprintf(cmdView, "\nCopy failed: %v", err)
			}
		} else {
			// 在命令窗口显示成功信息
			if cmdView, err := g.View("command"); err == nil {
				fmt.Fprintf(cmdView, "\nCopied selected text: %.30s...", selectedText)
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
							debugMsg := fmt.Sprintf("[DEBUG] Click outside area to close popup window: %s", lastPopup.ID)
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
		// 初始化搜索状态
		SearchMode:     false,              // 初始化搜索模式
		SearchTerm:     "",                 // 初始化搜索词
		SearchResults:  nil,                // 初始化搜索结果
		CurrentMatch:   -1,                 // 初始化当前匹配项
		SearchInput:    "",                 // 初始化搜索输入
		SearchDirty:    false,              // 初始化搜索脏标记
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
	
	// Ctrl+F启动搜索模式（在代码视图中）
	if err := g.SetKeybinding("code", gocui.KeyCtrlF, gocui.ModNone, startSearchHandler); err != nil {
		log.Panicln(err)
	}
	
	// F3跳转到下一个搜索结果
	if err := g.SetKeybinding("code", gocui.KeyF3, gocui.ModNone, jumpToNextMatchHandler); err != nil {
		log.Panicln(err)
	}
	
	// ESC键在代码视图中的专门处理（处理搜索模式退出）
	if err := g.SetKeybinding("code", gocui.KeyEsc, gocui.ModNone, handleSearchEscape); err != nil {
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
	
	// Enter键设置断点（在代码视图中，非搜索模式）
	if err := g.SetKeybinding("code", gocui.KeyEnter, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		if globalCtx != nil && globalCtx.SearchMode {
			return handleSearchEnter(g, v)
		}
		return handleBreakpointToggle(g, v)
	}); err != nil {
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
	
	// 在代码视图中添加搜索模式下的字符输入绑定
	searchChars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_."
	for _, ch := range searchChars {
		if err := g.SetKeybinding("code", ch, gocui.ModNone, func(ch rune) func(g *gocui.Gui, v *gocui.View) error {
			return func(g *gocui.Gui, v *gocui.View) error {
				if globalCtx != nil && globalCtx.SearchMode {
					return handleSearchCharInput(ch)(g, v)
				}
				return nil
			}
		}(ch)); err != nil {
			log.Printf("警告: 无法绑定搜索字符 %c: %v", ch, err)
		}
	}
	
	// 搜索模式下的退格键
	if err := g.SetKeybinding("code", gocui.KeyBackspace, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		if globalCtx != nil && globalCtx.SearchMode {
			return handleSearchBackspace(g, v)
		}
		return nil
	}); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("code", gocui.KeyBackspace2, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		if globalCtx != nil && globalCtx.SearchMode {
			return handleSearchBackspace(g, v)
		}
		return nil
	}); err != nil {
		log.Panicln(err)
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

// ========== 代码搜索功能 ==========

// 启动搜索模式
func startSearchMode(ctx *DebuggerContext) {
	if ctx == nil {
		return
	}
	ctx.SearchMode = true
	ctx.SearchInput = ""
	ctx.SearchTerm = ""
	ctx.SearchResults = nil
	ctx.CurrentMatch = -1
	ctx.SearchDirty = false
}

// 退出搜索模式
func exitSearchMode(ctx *DebuggerContext) {
	if ctx == nil {
		return
	}
	ctx.SearchMode = false
	ctx.SearchInput = ""
	ctx.SearchTerm = ""
	ctx.SearchResults = nil
	ctx.CurrentMatch = -1
	ctx.SearchDirty = false
}

// 执行搜索
func performSearch(ctx *DebuggerContext) {
	if ctx == nil || ctx.Project == nil || ctx.Project.CurrentFile == "" || ctx.SearchTerm == "" {
		ctx.SearchResults = nil
		ctx.CurrentMatch = -1
		return
	}
	
	// 获取当前文件内容
	lines, exists := ctx.Project.OpenFiles[ctx.Project.CurrentFile]
	if !exists {
		var err error
		lines, err = readFileContent(ctx.Project.CurrentFile)
		if err != nil {
			ctx.SearchResults = nil
			ctx.CurrentMatch = -1
			return
		}
		ctx.Project.OpenFiles[ctx.Project.CurrentFile] = lines
	}
	
	// 清空之前的搜索结果
	ctx.SearchResults = nil
	searchTerm := strings.ToLower(ctx.SearchTerm) // 大小写不敏感搜索
	
	// 在每一行中搜索
	for lineIdx, line := range lines {
		lineLower := strings.ToLower(line)
		startPos := 0
		
		// 在同一行中查找所有匹配项
		for {
			pos := strings.Index(lineLower[startPos:], searchTerm)
			if pos == -1 {
				break
			}
			
			actualPos := startPos + pos
			result := SearchResult{
				LineNumber:  lineIdx + 1, // 从1开始的行号
				StartColumn: actualPos,
				EndColumn:   actualPos + len(ctx.SearchTerm),
				Text:        line[actualPos:actualPos+len(ctx.SearchTerm)],
			}
			ctx.SearchResults = append(ctx.SearchResults, result)
			startPos = actualPos + 1 // 继续搜索下一个匹配项
		}
	}
	
	// 设置当前匹配项
	if len(ctx.SearchResults) > 0 {
		ctx.CurrentMatch = 0
	} else {
		ctx.CurrentMatch = -1
	}
}

// 跳转到下一个匹配项
func jumpToNextMatch(ctx *DebuggerContext) {
	if ctx == nil || len(ctx.SearchResults) == 0 {
		return
	}
	
	// 循环到下一个匹配项
	ctx.CurrentMatch = (ctx.CurrentMatch + 1) % len(ctx.SearchResults)
	
	// 滚动代码视图到匹配项所在行
	if ctx.CurrentMatch >= 0 && ctx.CurrentMatch < len(ctx.SearchResults) {
		targetLine := ctx.SearchResults[ctx.CurrentMatch].LineNumber
		// 将目标行设置为视图中心
		codeScroll = targetLine - 10 // 向上偏移10行，让匹配项显示在中间
		if codeScroll < 0 {
			codeScroll = 0
		}
	}
}

// 跳转到上一个匹配项
func jumpToPrevMatch(ctx *DebuggerContext) {
	if ctx == nil || len(ctx.SearchResults) == 0 {
		return
	}
	
	// 循环到上一个匹配项
	ctx.CurrentMatch = (ctx.CurrentMatch - 1 + len(ctx.SearchResults)) % len(ctx.SearchResults)
	
	// 滚动代码视图到匹配项所在行
	if ctx.CurrentMatch >= 0 && ctx.CurrentMatch < len(ctx.SearchResults) {
		targetLine := ctx.SearchResults[ctx.CurrentMatch].LineNumber
		// 将目标行设置为视图中心
		codeScroll = targetLine - 10 // 向上偏移10行，让匹配项显示在中间
		if codeScroll < 0 {
			codeScroll = 0
		}
	}
}

// 在文本中高亮搜索结果
func highlightSearchMatches(line string, lineNumber int, ctx *DebuggerContext) string {
	if ctx == nil || !ctx.SearchMode || ctx.SearchTerm == "" || len(ctx.SearchResults) == 0 {
		return line
	}
	
	// 找到当前行的所有匹配项
	var matches []SearchResult
	for _, result := range ctx.SearchResults {
		if result.LineNumber == lineNumber {
			matches = append(matches, result)
		}
	}
	
	if len(matches) == 0 {
		return line
	}
	
	// 从后往前处理匹配项，避免位置偏移问题
	result := line
	for i := len(matches) - 1; i >= 0; i-- {
		match := matches[i]
		
		// 检查是否是当前选中的匹配项
		isCurrentMatch := false
		if ctx.CurrentMatch >= 0 && ctx.CurrentMatch < len(ctx.SearchResults) {
			currentResult := ctx.SearchResults[ctx.CurrentMatch]
			if currentResult.LineNumber == match.LineNumber && 
			   currentResult.StartColumn == match.StartColumn {
				isCurrentMatch = true
			}
		}
		
		// 应用高亮样式
		before := result[:match.StartColumn]
		matchText := result[match.StartColumn:match.EndColumn]
		after := result[match.EndColumn:]
		
		if isCurrentMatch {
			// 当前匹配项使用红色背景
			result = before + "\x1b[41;37m" + matchText + "\x1b[0m" + after
		} else {
			// 其他匹配项使用黄色背景
			result = before + "\x1b[43;30m" + matchText + "\x1b[0m" + after
		}
	}
	
	return result
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


