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
}

var (
	focusNames = []string{"寄存器", "变量", "函数调用堆栈", "代码视图", "内存", "命令"}
	// 全局调试器上下文（原版gocui没有UserData字段）
	globalCtx *DebuggerContext
)

// ========== 窗口滚动状态 ==========
var (
	regScroll, varScroll, stackScroll, codeScroll, memScroll int
)

func layout(g *gocui.Gui) error {
	maxX, maxY := g.Size()
	// 状态栏
	if v, err := g.SetView("status", 0, 0, maxX-1, 2); err != nil && err != gocui.ErrUnknownView {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "状态"
	}
	// 寄存器窗口
	if v, err := g.SetView("registers", 0, 3, 28, maxY/3); err != nil && err != gocui.ErrUnknownView {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "寄存器"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	// 变量窗口
	if v, err := g.SetView("variables", 0, maxY/3+3, 28, 2*maxY/3); err != nil && err != gocui.ErrUnknownView {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "变量"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	// 调用栈窗口
	if v, err := g.SetView("stack", 0, 2*maxY/3+3, 28, maxY-6); err != nil && err != gocui.ErrUnknownView {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "函数调用堆栈"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	// 代码窗口
	if v, err := g.SetView("code", 29, 3, maxX-41, maxY-6); err != nil && err != gocui.ErrUnknownView {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "代码视图"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	// 内存窗口
	if v, err := g.SetView("memory", maxX-40, 3, maxX-1, maxY-6); err != nil && err != gocui.ErrUnknownView {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "内存"
		v.Highlight = true
		v.SelBgColor = gocui.ColorGreen
	}
	// 命令窗口
	if v, err := g.SetView("command", 0, maxY-5, maxX-1, maxY-1); err != nil && err != gocui.ErrUnknownView {
		if err != gocui.ErrUnknownView {
			return err
		}
		v.Title = "命令"
		v.Editable = true
	}
	return nil
}

func quit(g *gocui.Gui, v *gocui.View) error {
	return gocui.ErrQuit
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
	t := time.Now().Format("15:04:05")
	fmt.Fprintf(v, " 状态: %s   %s   函数: %s   地址: 0x%016x   %s\n",
		stateStr, bpfStr, ctx.CurrentFunc, ctx.CurrentAddr, t)
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

// ========== 内存窗口内容刷新 ==========
func updateMemoryView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("memory")
	if err != nil {
		return
	}
	v.Clear()
	if g.CurrentView() != nil && g.CurrentView().Name() == "memory" {
		fmt.Fprintln(v, "\x1b[43;30m▶ 内存 (已聚焦)\x1b[0m")
	} else {
		fmt.Fprintln(v, "内存")
	}
	base := ctx.CurrentAddr &^ 0xF
	for i := memScroll; i < memScroll+10; i++ {
		addr := base + uint64(i*16)
		fmt.Fprintf(v, "%016x: ", addr)
		for j := 0; j < 16; j += 4 {
			fmt.Fprintf(v, "%08x ", (addr+uint64(j))&0xffffffff)
		}
		fmt.Fprintln(v)
	}
}

// ========== 命令窗口内容刷新 ==========
func updateCommandView(g *gocui.Gui, ctx *DebuggerContext) {
	v, err := g.View("command")
	if err != nil {
		return
	}
	v.Clear()
	fmt.Fprintln(v, "快捷键:")
	fmt.Fprintln(v, "F5-继续  F10-下一步  F11-单步  Tab/`-切换窗口")
	fmt.Fprintln(v, "b-断点   c-继续     s-单步    r-重载BPF  q-退出")
	fmt.Fprintln(v, "1-寄存器 2-变量 3-堆栈 4-代码 5-内存 6-命令")
	fmt.Fprintln(v, "↑/↓-滚动当前窗口  PgUp/PgDn-快速滚动")
	
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
			viewNames := []string{"registers", "variables", "stack", "code", "memory", "command"}
			if i < len(viewNames) && viewNames[i] == currentView.Name() {
				fmt.Fprintf(v, "当前焦点: %s\n", name)
				break
			}
		}
	}
	
	if !ctx.BpfLoaded {
		fmt.Fprintln(v, "提示: BPF程序未加载，部分功能受限")
	}
	fmt.Fprintln(v, "\n命令: ")
}

// ========== 刷新所有窗口 ==========
func updateAllViews(g *gocui.Gui, ctx *DebuggerContext) {
	updateStatusView(g, ctx)
	updateRegistersView(g, ctx)
	updateVariablesView(g, ctx)
	updateStackView(g, ctx)
	updateCodeView(g, ctx)
	updateMemoryView(g, ctx)
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
	views := []string{"registers", "variables", "stack", "code", "memory", "command"}
	currentView := g.CurrentView()
	if currentView == nil {
		g.SetCurrentView("code")
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
	views := []string{"registers", "variables", "stack", "code", "memory", "command"}
	currentView := g.CurrentView()
	if currentView == nil {
		g.SetCurrentView("code")
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

func switchToMemory(g *gocui.Gui, v *gocui.View) error {
	g.SetCurrentView("memory")
	return nil
}

func switchToCommand(g *gocui.Gui, v *gocui.View) error {
	g.SetCurrentView("command")
	return nil
}

func scrollWindowByName(name string, direction int) {
	switch name {
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
	case "memory":
		memScroll += direction
		if memScroll < 0 {
			memScroll = 0
		}
	}
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
	if err := g.SetKeybinding("", '1', gocui.ModNone, switchToRegisters); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '2', gocui.ModNone, switchToVariables); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '3', gocui.ModNone, switchToStack); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '4', gocui.ModNone, switchToCode); err != nil {
		log.Panicln(err)
	}
	if err := g.SetKeybinding("", '5', gocui.ModNone, switchToMemory); err != nil {
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

	// Enter键选择当前行
	if err := g.SetKeybinding("", gocui.KeyEnter, gocui.ModNone, selectCurrentLine); err != nil {
		log.Panicln(err)
	}

	// 鼠标事件绑定
	viewNames := []string{"registers", "variables", "stack", "code", "memory"}
	
	for _, viewName := range viewNames {
		// 鼠标单击聚焦
		if err := g.SetKeybinding(viewName, gocui.MouseLeft, gocui.ModNone, mouseFocusHandler); err != nil {
			log.Panicln(err)
		}
		
		// 鼠标滚轮滚动
		if err := g.SetKeybinding(viewName, gocui.MouseWheelUp, gocui.ModNone, mouseScrollUpHandler); err != nil {
			log.Panicln(err)
		}
		if err := g.SetKeybinding(viewName, gocui.MouseWheelDown, gocui.ModNone, mouseScrollDownHandler); err != nil {
			log.Panicln(err)
		}
		
		// 鼠标拖拽选择（如果awesome-gocui支持）
		if err := g.SetKeybinding(viewName, gocui.MouseLeft, gocui.ModNone, mouseSelectStartHandler); err != nil {
			log.Printf("警告: 无法绑定鼠标拖拽开始事件: %v", err)
		}
		
		// 双击选择单词
		if err := g.SetKeybinding(viewName, gocui.MouseLeft, gocui.ModNone, selectWordAtCursor); err != nil {
			log.Printf("警告: 无法绑定双击选择事件: %v", err)
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
						if _, err := g.SetCurrentView("registers"); err == nil {
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


