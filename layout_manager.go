package main

import (
	"fmt"
	"github.com/jroimartin/gocui"
)

// ========== 布局管理器 ==========

// LayoutManager 布局管理器结构体
type LayoutManager struct {
	ctx *DebuggerContext
	gui *gocui.Gui
}

// NewLayoutManager 创建新的布局管理器
func NewLayoutManager(ctx *DebuggerContext, gui *gocui.Gui) *LayoutManager {
	return &LayoutManager{
		ctx: ctx,
		gui: gui,
	}
}

// ========== 动态布局系统 ==========

// InitDynamicLayout 初始化动态布局配置
func (lm *LayoutManager) InitDynamicLayout(maxX, maxY int) *DynamicLayout {
	return &DynamicLayout{
		LeftPanelWidth:    maxX / 4,     // 左侧面板占1/4宽度
		RightPanelWidth:   maxX / 3,     // 右侧面板占1/3宽度
		CommandHeight:     maxY / 4,     // 命令窗口占1/4高度
		RightPanelSplit1:  maxY / 3,     // 右侧第一个分割点
		RightPanelSplit2:  (maxY * 2) / 3, // 右侧第二个分割点
		IsDragging:        false,
		DragBoundary:      "",
		DragStartX:        0,
		DragStartY:        0,
		DragOriginalValue: 0,
	}
}

// DetectResizeBoundary 检测鼠标是否在可调整的边界附近
func (lm *LayoutManager) DetectResizeBoundary(x, y int, layout *DynamicLayout, maxX, maxY int) string {
	tolerance := 2 // 边界检测容差

	// 检测左侧面板右边界
	if x >= layout.LeftPanelWidth-tolerance && x <= layout.LeftPanelWidth+tolerance {
		return "left"
	}

	// 检测右侧面板左边界
	rightPanelStart := maxX - layout.RightPanelWidth
	if x >= rightPanelStart-tolerance && x <= rightPanelStart+tolerance {
		return "right"
	}

	// 检测命令窗口上边界
	commandStart := maxY - layout.CommandHeight
	if y >= commandStart-tolerance && y <= commandStart+tolerance {
		return "bottom"
	}

	// 检测右侧面板内部分割线
	if x >= rightPanelStart && x <= maxX {
		if y >= layout.RightPanelSplit1-tolerance && y <= layout.RightPanelSplit1+tolerance {
			return "right1"
		}
		if y >= layout.RightPanelSplit2-tolerance && y <= layout.RightPanelSplit2+tolerance {
			return "right2"
		}
	}

	return ""
}

// StartDrag 开始拖拽操作
func (lm *LayoutManager) StartDrag(boundary string, x, y int, layout *DynamicLayout) {
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

// HandleDragMove 处理拖拽移动
func (lm *LayoutManager) HandleDragMove(x, y int, layout *DynamicLayout, maxX, maxY int) {
	if !layout.IsDragging {
		return
	}

	deltaX := x - layout.DragStartX
	deltaY := y - layout.DragStartY

	switch layout.DragBoundary {
	case "left":
		newWidth := layout.DragOriginalValue + deltaX
		if newWidth >= 20 && newWidth <= maxX/2 {
			layout.LeftPanelWidth = newWidth
		}
	case "right":
		newWidth := layout.DragOriginalValue - deltaX
		if newWidth >= 20 && newWidth <= maxX/2 {
			layout.RightPanelWidth = newWidth
		}
	case "bottom":
		newHeight := layout.DragOriginalValue - deltaY
		if newHeight >= 5 && newHeight <= maxY/2 {
			layout.CommandHeight = newHeight
		}
	case "right1":
		newSplit := layout.DragOriginalValue + deltaY
		if newSplit >= 5 && newSplit < layout.RightPanelSplit2-5 {
			layout.RightPanelSplit1 = newSplit
		}
	case "right2":
		newSplit := layout.DragOriginalValue + deltaY
		commandStart := maxY - layout.CommandHeight
		if newSplit > layout.RightPanelSplit1+5 && newSplit < commandStart-5 {
			layout.RightPanelSplit2 = newSplit
		}
	}
}

// EndDrag 结束拖拽操作
func (lm *LayoutManager) EndDrag(layout *DynamicLayout) {
	layout.IsDragging = false
	layout.DragBoundary = ""
}

// GetBoundaryName 获取边界的友好名称
func (lm *LayoutManager) GetBoundaryName(boundary string) string {
	switch boundary {
	case "left":
		return "左侧面板边界"
	case "right":
		return "右侧面板边界"
	case "bottom":
		return "命令窗口边界"
	case "right1":
		return "寄存器/变量分割线"
	case "right2":
		return "变量/堆栈分割线"
	default:
		return ""
	}
}

// ========== 布局事件处理器 ==========

// ResetLayout 重置布局为默认设置
func (lm *LayoutManager) ResetLayout() error {
	maxX, maxY := lm.gui.Size()
	lm.ctx.Layout = lm.InitDynamicLayout(maxX, maxY)
	lm.ctx.CommandHistory = append(lm.ctx.CommandHistory, "布局已重置为默认设置")
	lm.ctx.CommandDirty = true
	return nil
}

// AdjustLeftPanel 增加左侧面板宽度
func (lm *LayoutManager) AdjustLeftPanel() error {
	if lm.ctx.Layout != nil {
		maxX, _ := lm.gui.Size()
		if lm.ctx.Layout.LeftPanelWidth < maxX/2 {
			lm.ctx.Layout.LeftPanelWidth += 5
			lm.ctx.CommandHistory = append(lm.ctx.CommandHistory, 
				fmt.Sprintf("左侧面板宽度: %d", lm.ctx.Layout.LeftPanelWidth))
			lm.ctx.CommandDirty = true
		}
	}
	return nil
}

// ShrinkLeftPanel 减少左侧面板宽度
func (lm *LayoutManager) ShrinkLeftPanel() error {
	if lm.ctx.Layout != nil {
		if lm.ctx.Layout.LeftPanelWidth > 20 {
			lm.ctx.Layout.LeftPanelWidth -= 5
			lm.ctx.CommandHistory = append(lm.ctx.CommandHistory, 
				fmt.Sprintf("左侧面板宽度: %d", lm.ctx.Layout.LeftPanelWidth))
			lm.ctx.CommandDirty = true
		}
	}
	return nil
}

// AdjustCommandHeight 增加命令窗口高度
func (lm *LayoutManager) AdjustCommandHeight() error {
	if lm.ctx.Layout != nil {
		_, maxY := lm.gui.Size()
		if lm.ctx.Layout.CommandHeight < maxY/2 {
			lm.ctx.Layout.CommandHeight += 2
			lm.ctx.CommandHistory = append(lm.ctx.CommandHistory, 
				fmt.Sprintf("命令窗口高度: %d", lm.ctx.Layout.CommandHeight))
			lm.ctx.CommandDirty = true
		}
	}
	return nil
}

// ShrinkCommandHeight 减少命令窗口高度
func (lm *LayoutManager) ShrinkCommandHeight() error {
	if lm.ctx.Layout != nil {
		if lm.ctx.Layout.CommandHeight > 5 {
			lm.ctx.Layout.CommandHeight -= 2
			lm.ctx.CommandHistory = append(lm.ctx.CommandHistory, 
				fmt.Sprintf("命令窗口高度: %d", lm.ctx.Layout.CommandHeight))
			lm.ctx.CommandDirty = true
		}
	}
	return nil
}

// ========== 全屏布局系统 ==========

// LayoutFullscreen 全屏布局
func (lm *LayoutManager) LayoutFullscreen(viewName string, maxX, maxY int) error {
	// 状态栏始终显示
	if v, err := lm.gui.SetView("status", 0, 0, maxX-1, 2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "状态"
	}
	
	// 全屏窗口占据状态栏下方的所有空间
	if v, err := lm.gui.SetView(viewName, 0, 3, maxX-1, maxY-1); err != nil {
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
	hideViews := []string{"filebrowser", "code", "registers", "variables", "stack", "command"}
	for _, name := range hideViews {
		if name != viewName {
			if v, err := lm.gui.SetView(name, -1, -1, 0, 0); err != nil && err != gocui.ErrUnknownView {
				return err
			} else if v != nil {
				v.Clear()
			}
		}
	}
	
	return nil
}

// ToggleFullscreen 切换全屏模式
func (lm *LayoutManager) ToggleFullscreen() error {
	if lm.ctx.IsFullscreen {
		// 退出全屏模式
		lm.ctx.IsFullscreen = false
		// 恢复布局
		if lm.ctx.SavedLayout != nil {
			lm.ctx.Layout = lm.ctx.SavedLayout
			lm.ctx.SavedLayout = nil
		}
		lm.ctx.FullscreenView = ""
		lm.ctx.CommandHistory = append(lm.ctx.CommandHistory, "已退出全屏模式")
	} else {
		// 进入全屏模式
		currentView := lm.gui.CurrentView()
		if currentView != nil {
			lm.ctx.IsFullscreen = true
			lm.ctx.FullscreenView = currentView.Name()
			// 保存当前布局
			if lm.ctx.Layout != nil {
				savedLayout := *lm.ctx.Layout
				lm.ctx.SavedLayout = &savedLayout
			}
			lm.ctx.CommandHistory = append(lm.ctx.CommandHistory, 
				fmt.Sprintf("已进入全屏模式: %s", currentView.Name()))
		}
	}
	lm.ctx.CommandDirty = true
	return nil
}

// EscapeExitFullscreen ESC键退出全屏
func (lm *LayoutManager) EscapeExitFullscreen() error {
	if lm.ctx.IsFullscreen {
		return lm.ToggleFullscreen()
	}
	
	// 如果不在全屏模式，检查是否在搜索模式
	if lm.ctx.SearchMode {
		lm.ctx.SearchMode = false
		lm.ctx.SearchInput = ""
		lm.ctx.SearchResults = nil
		lm.ctx.CurrentMatch = -1
		lm.ctx.CommandHistory = append(lm.ctx.CommandHistory, "退出搜索模式")
		lm.ctx.CommandDirty = true
		return nil
	}
	
	// 清空当前输入
	lm.ctx.CurrentInput = ""
	lm.ctx.CommandDirty = true
	return nil
} 