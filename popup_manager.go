package main

import (
	"fmt"
	"strings"
	"github.com/jroimartin/gocui"
)

// ========== 弹出窗口管理器 ==========

// PopupManager 弹出窗口管理器结构体
type PopupManager struct {
	ctx *DebuggerContext
	gui *gocui.Gui
}

// NewPopupManager 创建新的弹出窗口管理器
func NewPopupManager(ctx *DebuggerContext, gui *gocui.Gui) *PopupManager {
	return &PopupManager{
		ctx: ctx,
		gui: gui,
	}
}

// ========== 弹出窗口创建和管理 ==========

// CreatePopupWindow 创建弹出窗口
func (pm *PopupManager) CreatePopupWindow(id, title string, width, height int, content []string) *PopupWindow {
	maxX, maxY := pm.gui.Size()
	
	// 计算居中位置
	x := (maxX - width) / 2
	y := (maxY - height) / 2
	
	popup := &PopupWindow{
		ID:         id,
		Title:      title,
		X:          x,
		Y:          y,
		Width:      width,
		Height:     height,
		Content:    content,
		Visible:    true,
		Dragging:   false,
		DragStartX: 0,
		DragStartY: 0,
		ScrollY:    0,
	}
	
	return popup
}

// ShowPopupWindow 显示弹出窗口
func (pm *PopupManager) ShowPopupWindow(popup *PopupWindow) {
	pm.ctx.PopupWindows = append(pm.ctx.PopupWindows, popup)
}

// ClosePopupWindow 关闭弹出窗口
func (pm *PopupManager) ClosePopupWindow(id string) {
	for i, popup := range pm.ctx.PopupWindows {
		if popup.ID == id {
			// 从列表中移除
			pm.ctx.PopupWindows = append(pm.ctx.PopupWindows[:i], pm.ctx.PopupWindows[i+1:]...)
			
			// 删除对应的视图
			viewName := "popup_" + id
			pm.gui.DeleteView(viewName)
			break
		}
	}
}

// ClosePopupWindowWithView 通过视图关闭弹出窗口
func (pm *PopupManager) ClosePopupWindowWithView(viewName string) error {
	// 从视图名称提取弹出窗口ID
	if strings.HasPrefix(viewName, "popup_") {
		id := strings.TrimPrefix(viewName, "popup_")
		pm.ClosePopupWindow(id)
	}
	return nil
}

// FindPopupWindow 查找弹出窗口
func (pm *PopupManager) FindPopupWindow(id string) *PopupWindow {
	for _, popup := range pm.ctx.PopupWindows {
		if popup.ID == id {
			return popup
		}
	}
	return nil
}

// GetPopupWindowAt 获取指定位置的弹出窗口
func (pm *PopupManager) GetPopupWindowAt(x, y int) *PopupWindow {
	// 从后往前遍历，因为后面的窗口在上层
	for i := len(pm.ctx.PopupWindows) - 1; i >= 0; i-- {
		popup := pm.ctx.PopupWindows[i]
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

// IsInPopupTitleBar 检查坐标是否在弹出窗口标题栏内
func (pm *PopupManager) IsInPopupTitleBar(popup *PopupWindow, x, y int) bool {
	return x >= popup.X && x < popup.X+popup.Width &&
		   y >= popup.Y && y < popup.Y+2  // 标题栏通常是2行高
}

// ========== 弹出窗口渲染系统 ==========

// RenderPopupWindows 渲染所有弹出窗口
func (pm *PopupManager) RenderPopupWindows() error {
	for _, popup := range pm.ctx.PopupWindows {
		if !popup.Visible {
			continue
		}
		
		// 创建弹出窗口视图
		viewName := "popup_" + popup.ID
		if v, err := pm.gui.SetView(viewName, popup.X, popup.Y, popup.X+popup.Width, popup.Y+popup.Height); err != nil {
			if err != gocui.ErrUnknownView {
				return err
			}
			v.Title = popup.Title
			v.Wrap = true
			
			// 绑定鼠标事件
			pm.bindPopupMouseEvents(viewName)
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

// BindPopupMouseEvents 绑定弹出窗口鼠标事件
func (pm *PopupManager) bindPopupMouseEvents(viewName string) {
	// 鼠标点击事件
	pm.gui.SetKeybinding(viewName, gocui.MouseLeft, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		return pm.PopupMouseHandler(g, v)
	})
	
	// 鼠标滚轮事件
	pm.gui.SetKeybinding(viewName, gocui.MouseWheelUp, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		return pm.PopupScrollUpHandler(g, v)
	})
	
	pm.gui.SetKeybinding(viewName, gocui.MouseWheelDown, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		return pm.PopupScrollDownHandler(g, v)
	})
	
	// ESC键关闭弹出窗口
	pm.gui.SetKeybinding(viewName, gocui.KeyEsc, gocui.ModNone, func(g *gocui.Gui, v *gocui.View) error {
		return pm.PopupCloseHandler(g, v)
	})
}

// ========== 弹出窗口事件处理器 ==========

// PopupCloseHandler ESC键关闭弹出窗口处理器
func (pm *PopupManager) PopupCloseHandler(g *gocui.Gui, v *gocui.View) error {
	if v != nil {
		return pm.ClosePopupWindowWithView(v.Name())
	}
	return nil
}

// PopupMouseHandler 弹出窗口鼠标处理器
func (pm *PopupManager) PopupMouseHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	
	// 获取鼠标位置
	cx, cy := g.CurrentView().Cursor()
	ox, oy := v.Origin()
	x, y := cx+ox, cy+oy
	
	// 提取弹出窗口ID
	if strings.HasPrefix(v.Name(), "popup_") {
		id := strings.TrimPrefix(v.Name(), "popup_")
		popup := pm.FindPopupWindow(id)
		if popup != nil {
			// 检查是否点击在标题栏（用于拖拽）
			if pm.IsInPopupTitleBar(popup, x, y) {
				popup.Dragging = true
				popup.DragStartX = x
				popup.DragStartY = y
				pm.ctx.DraggingPopup = popup
			}
		}
	}
	
	return nil
}

// PopupScrollUpHandler 弹出窗口向上滚动处理器
func (pm *PopupManager) PopupScrollUpHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	
	// 提取弹出窗口ID
	if strings.HasPrefix(v.Name(), "popup_") {
		id := strings.TrimPrefix(v.Name(), "popup_")
		popup := pm.FindPopupWindow(id)
		if popup != nil && popup.ScrollY > 0 {
			popup.ScrollY--
		}
	}
	
	return nil
}

// PopupScrollDownHandler 弹出窗口向下滚动处理器
func (pm *PopupManager) PopupScrollDownHandler(g *gocui.Gui, v *gocui.View) error {
	if v == nil {
		return nil
	}
	
	// 提取弹出窗口ID
	if strings.HasPrefix(v.Name(), "popup_") {
		id := strings.TrimPrefix(v.Name(), "popup_")
		popup := pm.FindPopupWindow(id)
		if popup != nil {
			maxScroll := len(popup.Content) - (popup.Height - 2)
			if maxScroll > 0 && popup.ScrollY < maxScroll {
				popup.ScrollY++
			}
		}
	}
	
	return nil
}

// ========== 特殊弹出窗口 ==========

// ShowBreakpointsPopup 显示断点列表弹出窗口
func (pm *PopupManager) ShowBreakpointsPopup() {
	if pm.ctx.Project == nil {
		pm.ctx.CommandHistory = append(pm.ctx.CommandHistory, "错误: 未打开项目")
		pm.ctx.CommandDirty = true
		return
	}
	
	var content []string
	content = append(content, "断点列表:")
	content = append(content, "")
	
	if len(pm.ctx.Project.Breakpoints) == 0 {
		content = append(content, "暂无断点")
	} else {
		for i, bp := range pm.ctx.Project.Breakpoints {
			status := "启用"
			if !bp.Enabled {
				status = "禁用"
			}
			
			var location string
			if bp.Function != "" {
				location = fmt.Sprintf("函数: %s", bp.Function)
			} else {
				location = fmt.Sprintf("文件: %s, 行: %d", bp.File, bp.Line)
			}
			
			line := fmt.Sprintf("[%d] %s (%s)", i+1, location, status)
			content = append(content, line)
		}
	}
	
	content = append(content, "")
	content = append(content, "操作说明:")
	content = append(content, "- ESC: 关闭窗口")
	content = append(content, "- 鼠标滚轮: 滚动内容")
	
	popup := pm.CreatePopupWindow("breakpoints", "断点管理", 60, 20, content)
	pm.ShowPopupWindow(popup)
	
	pm.ctx.CommandHistory = append(pm.ctx.CommandHistory, 
		fmt.Sprintf("显示断点列表 (共%d个断点)", len(pm.ctx.Project.Breakpoints)))
	pm.ctx.CommandDirty = true
}

// ShowHelpPopup 显示帮助弹出窗口
func (pm *PopupManager) ShowHelpPopup() {
	uiManager := NewUIManager(pm.ctx, pm.gui)
	helpLines := uiManager.ShowHelp()
	
	popup := pm.CreatePopupWindow("help", "帮助", 80, 30, helpLines)
	pm.ShowPopupWindow(popup)
	
	pm.ctx.CommandHistory = append(pm.ctx.CommandHistory, "显示帮助信息")
	pm.ctx.CommandDirty = true
}

// ShowSearchResultsPopup 显示搜索结果弹出窗口
func (pm *PopupManager) ShowSearchResultsPopup() {
	if len(pm.ctx.SearchResults) == 0 {
		pm.ctx.CommandHistory = append(pm.ctx.CommandHistory, "无搜索结果")
		pm.ctx.CommandDirty = true
		return
	}
	
	var content []string
	content = append(content, fmt.Sprintf("搜索结果: \"%s\"", pm.ctx.SearchTerm))
	content = append(content, "")
	
	for i, result := range pm.ctx.SearchResults {
		marker := " "
		if i == pm.ctx.CurrentMatch {
			marker = ">"
		}
		line := fmt.Sprintf("%s [%d] %s", marker, result.LineNumber, result.Text)
		content = append(content, line)
	}
	
	content = append(content, "")
	content = append(content, "操作说明:")
	content = append(content, "- F3: 下一个匹配")
	content = append(content, "- Shift+F3: 上一个匹配")
	content = append(content, "- ESC: 关闭搜索")
	
	popup := pm.CreatePopupWindow("search_results", "搜索结果", 70, 25, content)
	pm.ShowPopupWindow(popup)
} 