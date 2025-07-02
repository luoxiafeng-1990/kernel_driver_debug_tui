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

var (
	focusNames = []string{"文件浏览器", "寄存器", "变量", "函数调用堆栈", "代码视图", "内存", "命令"}
	// 全局调试器上下文（原版gocui没有UserData字段）
	globalCtx *DebuggerContext
)

// ========== 窗口滚动状态 ==========
var (
	fileScroll, regScroll, varScroll, stackScroll, codeScroll, memScroll int
)

// ========== 动态布局系统 ==========

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
		err := filepath.Walk(rootPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // 忽略错误，继续处理其他文件
			}
			
			// 跳过根目录本身
			if path == rootPath {
				return nil
			}
			
			// 跳过隐藏文件和目录
			if strings.HasPrefix(info.Name(), ".") {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			
			// 只处理C/C++源文件和头文件
			if !info.IsDir() {
				ext := strings.ToLower(filepath.Ext(info.Name()))
				if ext != ".c" && ext != ".cpp" && ext != ".h" && ext != ".hpp" {
					return nil
				}
			}
			
			// 计算相对路径深度
			relPath, _ := filepath.Rel(rootPath, path)
			depth := strings.Count(relPath, string(filepath.Separator))
			
			// 限制深度避免过深的目录结构
			if depth > 3 {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			
			// 创建节点
			node := &FileNode{
				Name:     info.Name(),
				Path:     path,
				IsDir:    info.IsDir(),
				Children: make([]*FileNode, 0),
				Expanded: false,
			}
			
			// 添加到树中（简化实现，直接添加到根节点）
			root.Children = append(root.Children, node)
			
			return nil
		})
		
		if err != nil {
			return nil, err
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
	
	// 显示拖拽状态和提示
	if ctx.Layout != nil {
		if ctx.Layout.IsDragging {
			fmt.Fprintf(v, " | 🔧 正在调整: %s", getBoundaryName(ctx.Layout.DragBoundary))
		} else {
			fmt.Fprint(v, " | 💡 提示: 鼠标拖拽窗口边界调整大小, Ctrl+R重置布局")
		}
		
		// 显示当前布局参数
		fmt.Fprintf(v, " | 布局: L%d R%d C%d", 
			ctx.Layout.LeftPanelWidth, 
			ctx.Layout.RightPanelWidth, 
			ctx.Layout.CommandHeight)
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
	fmt.Fprintln(v, "")
	
	// 显示文件树
	if ctx.Project.FileTree != nil {
		displayFileTree(v, ctx.Project.FileTree, 0, fileScroll)
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
		
		fmt.Fprintf(v, "文件: %s\n", filepath.Base(ctx.Project.CurrentFile))
		fmt.Fprintln(v, "")
		
		// 显示代码行
		maxLines := len(lines)
		startLine := codeScroll
		if startLine >= maxLines {
			startLine = maxLines - 1
		}
		if startLine < 0 {
			startLine = 0
		}
		
		for i := startLine; i < maxLines && i < startLine+20; i++ {
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
		
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "操作: Enter-设置断点  Space-打开文件")
		
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
		for i := codeScroll; i < len(insts); i++ {
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
			lines := strings.Split(strings.TrimSuffix(viewBuffer, "\n"), "\n")
			
			// 查找当前输入行（以 "> " 开头的最后一行）
			var actualInput string
			for i := len(lines) - 1; i >= 0; i-- {
				line := lines[i]
				if strings.HasPrefix(line, "> ") {
					actualInput = line[2:] // 去掉 "> " 前缀
					break
				}
			}
			
			// 如果实际输入与CurrentInput不同，说明有粘贴操作
			if actualInput != ctx.CurrentInput {
				ctx.CurrentInput = actualInput
				ctx.CommandDirty = true // 标记需要重新同步光标位置
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
		
		fmt.Fprintln(v, "命令终端 - 按6聚焦")
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

// 处理文件选择
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
			"  generate     - 生成BPF调试代码",
			"  breakpoint   - 清除所有断点",
			"  close        - 关闭当前项目",
			"  pwd          - 显示当前工作目录",
			"",
			"导航快捷键:",
			"  Tab - 切换窗口",
			"  1-6 - 直接切换到指定窗口",
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
			// 如果是相对路径，转换为绝对路径
			if !filepath.IsAbs(projectPath) {
				wd, _ := os.Getwd()
				projectPath = filepath.Join(wd, projectPath)
			}
			
			project, err := openProject(projectPath)
			if err != nil {
				output = []string{fmt.Sprintf("错误: 打开项目失败: %v", err)}
			} else {
				globalCtx.Project = project
				output = []string{
					fmt.Sprintf("成功打开项目: %s", filepath.Base(projectPath)),
					fmt.Sprintf("找到 %d 个文件", countFiles(project.FileTree)),
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
		
	case "breakpoint", "bp":
		if globalCtx.Project != nil {
			count := len(globalCtx.Project.Breakpoints)
			globalCtx.Project.Breakpoints = make([]Breakpoint, 0)
			output = []string{fmt.Sprintf("成功: 已清除 %d 个断点", count)}
		} else {
			output = []string{"提示: 没有打开的项目"}
		}
		
	case "close":
		if globalCtx.Project != nil {
			projectName := filepath.Base(globalCtx.Project.RootPath)
			globalCtx.Project = nil
			output = []string{fmt.Sprintf("成功: 已关闭项目 %s", projectName)}
		} else {
			output = []string{"提示: 没有打开的项目"}
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

// 处理字符输入
func handleCharInput(ch rune) func(g *gocui.Gui, v *gocui.View) error {
	return func(g *gocui.Gui, v *gocui.View) error {
		if globalCtx == nil {
			return nil
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
	if globalCtx == nil || globalCtx.Layout == nil {
		return mouseFocusHandler(g, v) // 回退到普通聚焦处理
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
		
		// 检测是否在可拖拽边界上
		boundary := detectResizeBoundary(mouseX, mouseY, globalCtx.Layout, maxX, maxY)
		if boundary != "" {
			startDrag(boundary, mouseX, mouseY, globalCtx.Layout)
			return nil
		}
	}
	
	// 如果不是拖拽，执行普通的聚焦处理
	return mouseFocusHandler(g, v)
}

// 鼠标拖拽处理
func mouseDragResizeHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx == nil || globalCtx.Layout == nil || !globalCtx.Layout.IsDragging {
		return nil
	}
	
	maxX, maxY := g.Size()
	
	// 获取当前鼠标位置（简化实现）
	if v != nil {
		ox, oy := v.Origin()
		cx, cy := v.Cursor()
		mouseX := ox + cx
		mouseY := oy + cy
		
		// 处理拖拽移动
		handleDragMove(mouseX, mouseY, globalCtx.Layout, maxX, maxY)
	}
	
	return nil
}

// 鼠标释放处理 - 结束拖拽
func mouseUpHandler(g *gocui.Gui, v *gocui.View) error {
	if globalCtx != nil && globalCtx.Layout != nil && globalCtx.Layout.IsDragging {
		endDrag(globalCtx.Layout)
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

	// 数字键直接切换窗口
	if err := g.SetKeybinding("", '1', gocui.ModNone, switchToFileBrowser); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '2', gocui.ModNone, switchToRegisters); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '3', gocui.ModNone, switchToVariables); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '4', gocui.ModNone, switchToStack); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '5', gocui.ModNone, switchToCode); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '6', gocui.ModNone, switchToCommand); err != nil {
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

	// F2键文件选择（避免与命令窗口字符冲突）
	if err := g.SetKeybinding("filebrowser", gocui.KeyF2, gocui.ModNone, handleFileSelection); err != nil {
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
	
	// Escape键清空当前输入（在命令窗口中）
	if err := g.SetKeybinding("command", gocui.KeyEsc, gocui.ModNone, clearCurrentInput); err != nil {
		log.Panicln(err)
	}
	
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
	chars := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 ./-_:="
	for _, ch := range chars {
		if err := g.SetKeybinding("command", ch, gocui.ModNone, handleCharInput(ch)); err != nil {
			log.Printf("警告: 无法绑定字符 %c: %v", ch, err)
		}
	}

	// 鼠标事件绑定
	viewNames := []string{"filebrowser", "registers", "variables", "stack", "code", "command"}
	
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


