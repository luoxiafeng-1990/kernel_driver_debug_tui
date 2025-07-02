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

	"github.com/jroimartin/gocui"
	"github.com/aymanbagabas/go-osc52/v2"
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

func layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	
	// 状态栏
	if v, err := g.SetView("status", 0, 0, maxX-1, 2); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "状态"
	}
	
	// 文件浏览器窗口 (左侧)
	if v, err := g.SetView("filebrowser", 0, 3, 35, maxY-6); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "文件浏览器"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 寄存器窗口 (右上)
	if v, err := g.SetView("registers", maxX-35, 3, maxX-1, maxY/3); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "寄存器"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 变量窗口 (右中)
	if v, err := g.SetView("variables", maxX-35, maxY/3+1, maxX-1, 2*maxY/3); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "变量"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 调用栈窗口 (右下)
	if v, err := g.SetView("stack", maxX-35, 2*maxY/3+1, maxX-1, maxY-6); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "函数调用堆栈"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 代码窗口 (中央) - 修复右边界，为命令窗口留出空间
	if v, err := g.SetView("code", 36, 3, maxX-36, maxY-6); err != nil {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "代码视图"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	
	// 命令窗口 (底部) - 修复布局，确保不与其他窗口重叠
	if v, err := g.SetView("command", 0, maxY-5, maxX-1, maxY-1); err != nil {
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
	stateStr := "未知"
	switch ctx.State {
	case DEBUG_STOPPED:
		stateStr = "已停止"
	case DEBUG_RUNNING:
		stateStr = "运行中"
	case DEBUG_STEPPING:
		stateStr = "单步执行"
	case DEBUG_BREAKPOINT:
		stateStr = "断点"
	}
	bpfStr := "BPF: ✗"
	if ctx.BpfLoaded {
		bpfStr = "BPF: ✓"
	}
	
	projectStr := "项目: 未打开"
	if ctx.Project != nil {
		projectStr = fmt.Sprintf("项目: %s", filepath.Base(ctx.Project.RootPath))
		if ctx.Project.CurrentFile != "" {
			projectStr += fmt.Sprintf(" | 文件: %s", filepath.Base(ctx.Project.CurrentFile))
		}
		if len(ctx.Project.Breakpoints) > 0 {
			projectStr += fmt.Sprintf(" | 断点: %d", len(ctx.Project.Breakpoints))
		}
	}
	
	t := time.Now().Format("15:04:05")
	fmt.Fprintf(v, " 状态: %s   %s   %s   %s\n",
		stateStr, bpfStr, projectStr, t)
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
	
	// 只有在命令窗口不是当前聚焦窗口时才清空和重新填充内容
	// 这样可以保持用户正在输入的命令
	if g.CurrentView() == nil || g.CurrentView().Name() != "command" {
		v.Clear()
		
		fmt.Fprintln(v, "命令窗口 - 按6或点击这里聚焦")
		fmt.Fprintln(v, "")
		fmt.Fprintln(v, "快捷键:")
		fmt.Fprintln(v, "Tab/`-切换窗口  ↑/↓-滚动  Enter-选择/设置断点")
		fmt.Fprintln(v, "Space-打开文件  g-生成BPF  c-清除断点  q-退出")
		fmt.Fprintln(v, "1-文件浏览器 2-寄存器 3-变量 4-断点 5-代码 6-命令")
	
		// 显示鼠标支持状态
		if ctx.MouseEnabled {
			fmt.Fprintln(v, "鼠标: ✓ 支持点击切换焦点和滚轮滚动")
		} else {
			fmt.Fprintln(v, "鼠标: ✗ 不支持，请使用键盘操作")
		}
		
		// 显示当前焦点
		currentView := g.CurrentView()
		if currentView != nil {
			for i, name := range focusNames {
				viewNames := []string{"filebrowser", "registers", "variables", "stack", "code", "command"}
				if i < len(viewNames) && viewNames[i] == currentView.Name() {
					fmt.Fprintf(v, "当前焦点: %s\n", name)
					break
				}
			}
		}
		
		// 项目状态
		if ctx.Project == nil {
			fmt.Fprintln(v, "")
			fmt.Fprintln(v, "命令示例:")
			fmt.Fprintln(v, "open ../tacosys_ko  - 打开项目")
			fmt.Fprintln(v, "open /path/to/project - 打开指定项目")
		} else {
			fmt.Fprintln(v, "")
			fmt.Fprintln(v, "项目命令:")
			fmt.Fprintln(v, "generate - 生成BPF代码")
			fmt.Fprintln(v, "clear - 清除所有断点")
			fmt.Fprintln(v, "close - 关闭项目")
		}
		
		fmt.Fprintln(v, "\n命令: ")
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
	// 使用OSC52序列复制到剪贴板
	osc52Seq := osc52.New(text)
	_, err := osc52Seq.WriteTo(os.Stderr)
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
	
	// 获取命令内容
	content := strings.TrimSpace(v.Buffer())
	if content == "" {
		return nil
	}
	
	// 解析命令
	parts := strings.Fields(content)
	if len(parts) == 0 {
		return nil
	}
	
	command := parts[0]
	
	switch command {
	case "open":
		if len(parts) < 2 {
			v.Clear()
			fmt.Fprintln(v, "用法: open <项目路径>")
			return nil
		}
		
		projectPath := parts[1]
		// 如果是相对路径，转换为绝对路径
		if !filepath.IsAbs(projectPath) {
			wd, _ := os.Getwd()
			projectPath = filepath.Join(wd, projectPath)
		}
		
		project, err := openProject(projectPath)
		if err != nil {
			v.Clear()
			fmt.Fprintf(v, "打开项目失败: %v\n", err)
		} else {
			globalCtx.Project = project
			v.Clear()
			fmt.Fprintf(v, "成功打开项目: %s\n", filepath.Base(projectPath))
		}
		
	case "generate", "g":
		if globalCtx.Project == nil {
			v.Clear()
			fmt.Fprintln(v, "请先打开项目")
			return nil
		}
		
		err := generateBPF(globalCtx)
		if err != nil {
			v.Clear()
			fmt.Fprintf(v, "生成BPF失败: %v\n", err)
		} else {
			v.Clear()
			fmt.Fprintln(v, "BPF代码生成成功: debug_breakpoints.bpf.c")
			globalCtx.BpfLoaded = true
		}
		
	case "clear", "c":
		if globalCtx.Project != nil {
			globalCtx.Project.Breakpoints = make([]Breakpoint, 0)
			v.Clear()
			fmt.Fprintln(v, "已清除所有断点")
		}
		
	case "close":
		globalCtx.Project = nil
		v.Clear()
		fmt.Fprintln(v, "已关闭项目")
		
	case "help", "h":
		v.Clear()
		fmt.Fprintln(v, "可用命令:")
		fmt.Fprintln(v, "open <路径> - 打开项目")
		fmt.Fprintln(v, "generate - 生成BPF代码")
		fmt.Fprintln(v, "clear - 清除断点")
		fmt.Fprintln(v, "close - 关闭项目")
		
	default:
		v.Clear()
		fmt.Fprintf(v, "未知命令: %s\n", command)
		fmt.Fprintln(v, "输入 help 查看可用命令")
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

func main() {
	// 创建调试器上下文
	ctx := &DebuggerContext{
		State:        DEBUG_STOPPED,
		CurrentFocus: 0,
		BpfLoaded:    false,
		CurrentFunc:  "main",
		CurrentAddr:  0x400000,
		Running:      false,
		MouseEnabled: false,
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

	// Space键文件选择
	if err := g.SetKeybinding("filebrowser", gocui.KeySpace, gocui.ModNone, handleFileSelection); err != nil {
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
	
	// g键生成BPF
	if err := g.SetKeybinding("", 'g', gocui.ModNone, generateBPFHandler); err != nil {
		log.Panicln(err)
	}
	
	// c键清除断点
	if err := g.SetKeybinding("", 'c', gocui.ModNone, clearBreakpointsHandler); err != nil {
		log.Panicln(err)
	}

	// 鼠标事件绑定
	viewNames := []string{"filebrowser", "registers", "variables", "stack", "code", "command"}
	
	for _, viewName := range viewNames {
		// 鼠标单击聚焦
		if err := g.SetKeybinding(viewName, gocui.MouseLeft, gocui.ModNone, mouseFocusHandler); err != nil {
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


