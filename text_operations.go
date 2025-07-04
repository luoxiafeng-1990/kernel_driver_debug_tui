package main

import (
	"fmt"
	"strings"
	"os/exec"
	"path/filepath"
	
	"github.com/jroimartin/gocui"
)

// ========== 文本操作管理器 ==========

type TextOperationsManager struct {
	ctx *DebuggerContext
	gui *gocui.Gui
	// 文本选择状态
	selectionStartLine int
	selectionStartCol  int
	selectionEndLine   int
	selectionEndCol    int
	isSelecting        bool
	selectedText       string
	// 剪贴板
	clipboardContent   string
}

// NewTextOperationsManager 创建文本操作管理器
func NewTextOperationsManager(ctx *DebuggerContext, gui *gocui.Gui) *TextOperationsManager {
	return &TextOperationsManager{
		ctx: ctx,
		gui: gui,
		selectionStartLine: -1,
		selectionStartCol:  -1,
		selectionEndLine:   -1,
		selectionEndCol:    -1,
		isSelecting:        false,
		selectedText:       "",
		clipboardContent:   "",
	}
}

// ========== 文本选择功能 ==========

// StartSelection 开始文本选择
func (tom *TextOperationsManager) StartSelection(g *gocui.Gui, v *gocui.View) error {
	if v.Name() != "code" {
		return nil
	}
	
	// 获取当前光标位置
	cx, cy := v.Cursor()
	
	// 转换为实际的代码行列位置
	actualLine, actualCol := tom.viewPosToCodePos(cx, cy)
	
	tom.selectionStartLine = actualLine
	tom.selectionStartCol = actualCol
	tom.selectionEndLine = actualLine
	tom.selectionEndCol = actualCol
	tom.isSelecting = true
	
	return nil
}

// UpdateSelection 更新文本选择
func (tom *TextOperationsManager) UpdateSelection(g *gocui.Gui, v *gocui.View) error {
	if !tom.isSelecting || v.Name() != "code" {
		return nil
	}
	
	// 获取当前光标位置
	cx, cy := v.Cursor()
	
	// 转换为实际的代码行列位置
	actualLine, actualCol := tom.viewPosToCodePos(cx, cy)
	
	tom.selectionEndLine = actualLine
	tom.selectionEndCol = actualCol
	
	// 更新选中文本
	tom.updateSelectedText()
	
	return nil
}

// EndSelection 结束文本选择
func (tom *TextOperationsManager) EndSelection(g *gocui.Gui, v *gocui.View) error {
	if !tom.isSelecting {
		return nil
	}
	
	tom.isSelecting = false
	
	// 如果有选中文本，保存到剪贴板
	if tom.selectedText != "" {
		tom.clipboardContent = tom.selectedText
		
		// 尝试复制到系统剪贴板
		tom.copyToSystemClipboard(tom.selectedText)
		
		// 在命令历史中添加提示
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("Selected text copied to clipboard (%d chars)", len(tom.selectedText)))
		tom.ctx.CommandDirty = true
	}
	
	return nil
}

// ClearSelection 清除选择
func (tom *TextOperationsManager) ClearSelection() {
	tom.selectionStartLine = -1
	tom.selectionStartCol = -1
	tom.selectionEndLine = -1
	tom.selectionEndCol = -1
	tom.isSelecting = false
	tom.selectedText = ""
}

// ========== 复制粘贴功能 ==========

// CopySelection 复制选中文本
func (tom *TextOperationsManager) CopySelection(g *gocui.Gui, v *gocui.View) error {
	if tom.selectedText == "" {
		// 如果没有选中文本，复制当前行
		return tom.CopyCurrentLine(g, v)
	}
	
	tom.clipboardContent = tom.selectedText
	tom.copyToSystemClipboard(tom.selectedText)
	
	tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
		fmt.Sprintf("Copied to clipboard: %s", tom.truncateText(tom.selectedText, 50)))
	tom.ctx.CommandDirty = true
	
	return nil
}

// CopyCurrentLine 复制当前行
func (tom *TextOperationsManager) CopyCurrentLine(g *gocui.Gui, v *gocui.View) error {
	if v.Name() != "code" || tom.ctx.Project == nil || tom.ctx.Project.CurrentFile == "" {
		return nil
	}
	
	// 获取当前行
	_, cy := v.Cursor()
	actualLine, _ := tom.viewPosToCodePos(0, cy)
	
	// 获取文件内容
	lines, exists := tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile]
	if !exists {
		fileManager := NewFileManager(tom.ctx)
		content, err := fileManager.GetCurrentFileContent()
		if err != nil {
			return err
		}
		lines = content
		tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	}
	
	if actualLine >= 0 && actualLine < len(lines) {
		lineText := lines[actualLine]
		tom.clipboardContent = lineText
		tom.copyToSystemClipboard(lineText)
		
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("Copied line %d: %s", actualLine+1, tom.truncateText(lineText, 50)))
		tom.ctx.CommandDirty = true
	}
	
	return nil
}

// PasteFromClipboard 从剪贴板粘贴
func (tom *TextOperationsManager) PasteFromClipboard(g *gocui.Gui, v *gocui.View) error {
	if v.Name() != "command" {
		return nil
	}
	
	// 尝试从系统剪贴板获取内容
	systemClipboard := tom.getFromSystemClipboard()
	if systemClipboard != "" {
		tom.clipboardContent = systemClipboard
	}
	
	if tom.clipboardContent != "" {
		// 将剪贴板内容添加到当前输入
		tom.ctx.CurrentInput += tom.clipboardContent
		tom.ctx.CommandDirty = true
		
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("Pasted from clipboard: %s", tom.truncateText(tom.clipboardContent, 50)))
	}
	
	return nil
}

// ========== 文本查找替换功能 ==========

// FindAndReplace 查找并替换文本
func (tom *TextOperationsManager) FindAndReplace(findText, replaceText string, replaceAll bool) error {
	if tom.ctx.Project == nil || tom.ctx.Project.CurrentFile == "" {
		return fmt.Errorf("no file open")
	}
	
	// 获取文件内容
	lines, exists := tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile]
	if !exists {
		fileManager := NewFileManager(tom.ctx)
		content, err := fileManager.GetCurrentFileContent()
		if err != nil {
			return err
		}
		lines = content
		tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	}
	
	replacedCount := 0
	
	for i, line := range lines {
		if replaceAll {
			// 替换所有匹配
			newLine := strings.ReplaceAll(line, findText, replaceText)
			if newLine != line {
				lines[i] = newLine
				replacedCount++
			}
		} else {
			// 只替换第一个匹配
			if strings.Contains(line, findText) {
				lines[i] = strings.Replace(line, findText, replaceText, 1)
				replacedCount++
				break
			}
		}
	}
	
	if replacedCount > 0 {
		// 更新文件内容
		tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
		
		// 标记文件已修改
		tom.ctx.Project.ModifiedFiles = append(tom.ctx.Project.ModifiedFiles, tom.ctx.Project.CurrentFile)
		
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("Replaced %d occurrences of '%s' with '%s'", replacedCount, findText, replaceText))
		tom.ctx.CommandDirty = true
	}
	
	return nil
}

// ========== 文本编辑功能 ==========

// InsertText 插入文本
func (tom *TextOperationsManager) InsertText(g *gocui.Gui, v *gocui.View, text string) error {
	if v.Name() != "code" || tom.ctx.Project == nil || tom.ctx.Project.CurrentFile == "" {
		return nil
	}
	
	// 获取当前光标位置
	cx, cy := v.Cursor()
	actualLine, actualCol := tom.viewPosToCodePos(cx, cy)
	
	// 获取文件内容
	lines, exists := tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile]
	if !exists {
		fileManager := NewFileManager(tom.ctx)
		content, err := fileManager.GetCurrentFileContent()
		if err != nil {
			return err
		}
		lines = content
		tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	}
	
	if actualLine >= 0 && actualLine < len(lines) {
		line := lines[actualLine]
		
		// 插入文本
		if actualCol >= 0 && actualCol <= len(line) {
			newLine := line[:actualCol] + text + line[actualCol:]
			lines[actualLine] = newLine
			
			// 更新文件内容
			tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
			
			// 标记文件已修改
			tom.ctx.Project.ModifiedFiles = append(tom.ctx.Project.ModifiedFiles, tom.ctx.Project.CurrentFile)
		}
	}
	
	return nil
}

// DeleteText 删除文本
func (tom *TextOperationsManager) DeleteText(g *gocui.Gui, v *gocui.View, startLine, startCol, endLine, endCol int) error {
	if v.Name() != "code" || tom.ctx.Project == nil || tom.ctx.Project.CurrentFile == "" {
		return nil
	}
	
	// 获取文件内容
	lines, exists := tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile]
	if !exists {
		fileManager := NewFileManager(tom.ctx)
		content, err := fileManager.GetCurrentFileContent()
		if err != nil {
			return err
		}
		lines = content
		tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	}
	
	// 执行删除操作
	if startLine == endLine {
		// 同一行删除
		if startLine >= 0 && startLine < len(lines) {
			line := lines[startLine]
			if startCol >= 0 && endCol <= len(line) && startCol <= endCol {
				newLine := line[:startCol] + line[endCol:]
				lines[startLine] = newLine
			}
		}
	} else {
		// 跨行删除
		if startLine >= 0 && startLine < len(lines) && endLine >= 0 && endLine < len(lines) {
			startLineText := lines[startLine][:startCol]
			endLineText := lines[endLine][endCol:]
			
			// 合并首尾行
			newLine := startLineText + endLineText
			
			// 删除中间行
			newLines := []string{}
			for i, line := range lines {
				if i < startLine || i > endLine {
					newLines = append(newLines, line)
				} else if i == startLine {
					newLines = append(newLines, newLine)
				}
			}
			
			lines = newLines
		}
	}
	
	// 更新文件内容
	tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	
	// 标记文件已修改
	tom.ctx.Project.ModifiedFiles = append(tom.ctx.Project.ModifiedFiles, tom.ctx.Project.CurrentFile)
	
	return nil
}

// ========== 文本格式化功能 ==========

// FormatCode 格式化代码
func (tom *TextOperationsManager) FormatCode(g *gocui.Gui, v *gocui.View) error {
	if tom.ctx.Project == nil || tom.ctx.Project.CurrentFile == "" {
		return nil
	}
	
	// 根据文件类型选择格式化工具
	ext := strings.ToLower(filepath.Ext(tom.ctx.Project.CurrentFile))
	
	switch ext {
	case ".go":
		return tom.formatGoCode()
	case ".c", ".cpp", ".h", ".hpp":
		return tom.formatCCode()
	case ".py":
		return tom.formatPythonCode()
	case ".js", ".ts":
		return tom.formatJavaScriptCode()
	default:
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("No formatter available for %s files", ext))
		tom.ctx.CommandDirty = true
		return nil
	}
}

// formatGoCode 格式化Go代码
func (tom *TextOperationsManager) formatGoCode() error {
	// 使用gofmt格式化
	cmd := exec.Command("gofmt", tom.ctx.Project.CurrentFile)
	output, err := cmd.Output()
	if err != nil {
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("gofmt error: %v", err))
		tom.ctx.CommandDirty = true
		return err
	}
	
	// 更新文件内容
	lines := strings.Split(string(output), "\n")
	tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	
	tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
		"Go code formatted successfully")
	tom.ctx.CommandDirty = true
	
	return nil
}

// formatCCode 格式化C代码
func (tom *TextOperationsManager) formatCCode() error {
	// 使用clang-format格式化
	cmd := exec.Command("clang-format", tom.ctx.Project.CurrentFile)
	output, err := cmd.Output()
	if err != nil {
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("clang-format error: %v", err))
		tom.ctx.CommandDirty = true
		return err
	}
	
	// 更新文件内容
	lines := strings.Split(string(output), "\n")
	tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	
	tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
		"C code formatted successfully")
	tom.ctx.CommandDirty = true
	
	return nil
}

// formatPythonCode 格式化Python代码
func (tom *TextOperationsManager) formatPythonCode() error {
	// 使用autopep8格式化
	cmd := exec.Command("autopep8", "--in-place", tom.ctx.Project.CurrentFile)
	err := cmd.Run()
	if err != nil {
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("autopep8 error: %v", err))
		tom.ctx.CommandDirty = true
		return err
	}
	
	// 重新读取文件
	fileManager := NewFileManager(tom.ctx)
	lines, err := fileManager.GetCurrentFileContent()
	if err != nil {
		return err
	}
	
	tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	
	tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
		"Python code formatted successfully")
	tom.ctx.CommandDirty = true
	
	return nil
}

// formatJavaScriptCode 格式化JavaScript代码
func (tom *TextOperationsManager) formatJavaScriptCode() error {
	// 使用prettier格式化
	cmd := exec.Command("prettier", "--write", tom.ctx.Project.CurrentFile)
	err := cmd.Run()
	if err != nil {
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			fmt.Sprintf("prettier error: %v", err))
		tom.ctx.CommandDirty = true
		return err
	}
	
	// 重新读取文件
	fileManager := NewFileManager(tom.ctx)
	lines, err := fileManager.GetCurrentFileContent()
	if err != nil {
		return err
	}
	
	tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	
	tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
		"JavaScript code formatted successfully")
	tom.ctx.CommandDirty = true
	
	return nil
}

// ========== 文本统计功能 ==========

// GetTextStats 获取文本统计信息
func (tom *TextOperationsManager) GetTextStats() map[string]interface{} {
	stats := make(map[string]interface{})
	
	if tom.ctx.Project == nil || tom.ctx.Project.CurrentFile == "" {
		return stats
	}
	
	// 获取文件内容
	lines, exists := tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile]
	if !exists {
		fileManager := NewFileManager(tom.ctx)
		content, err := fileManager.GetCurrentFileContent()
		if err != nil {
			return stats
		}
		lines = content
		tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile] = lines
	}
	
	totalLines := len(lines)
	totalChars := 0
	totalWords := 0
	nonEmptyLines := 0
	
	for _, line := range lines {
		totalChars += len(line)
		if strings.TrimSpace(line) != "" {
			nonEmptyLines++
		}
		words := strings.Fields(line)
		totalWords += len(words)
	}
	
	stats["total_lines"] = totalLines
	stats["non_empty_lines"] = nonEmptyLines
	stats["total_characters"] = totalChars
	stats["total_words"] = totalWords
	stats["file_size"] = totalChars // 近似文件大小
	
	return stats
}

// ShowTextStats 显示文本统计信息
func (tom *TextOperationsManager) ShowTextStats(g *gocui.Gui, v *gocui.View) error {
	stats := tom.GetTextStats()
	
	if len(stats) == 0 {
		tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, 
			"No file open for statistics")
		tom.ctx.CommandDirty = true
		return nil
	}
	
	statsInfo := fmt.Sprintf("File statistics: %d lines (%d non-empty), %d words, %d characters",
		stats["total_lines"], stats["non_empty_lines"], stats["total_words"], stats["total_characters"])
	
	tom.ctx.CommandHistory = append(tom.ctx.CommandHistory, statsInfo)
	tom.ctx.CommandDirty = true
	
	return nil
}

// ========== 辅助函数 ==========

// viewPosToCodePos 将视图位置转换为代码位置
func (tom *TextOperationsManager) viewPosToCodePos(viewX, viewY int) (int, int) {
	// 考虑标题行和滚动偏移
	headerLines := 2
	codeLine := viewY - headerLines + codeScroll
	
	// 考虑行号显示（"  123: "）
	codeCol := viewX - 6 // 6 = 3位行号 + ": " + 1个空格
	if codeCol < 0 {
		codeCol = 0
	}
	
	return codeLine, codeCol
}

// updateSelectedText 更新选中的文本
func (tom *TextOperationsManager) updateSelectedText() {
	if tom.ctx.Project == nil || tom.ctx.Project.CurrentFile == "" {
		return
	}
	
	// 获取文件内容
	lines, exists := tom.ctx.Project.OpenFiles[tom.ctx.Project.CurrentFile]
	if !exists {
		return
	}
	
	// 确保选择范围有效
	startLine := tom.selectionStartLine
	endLine := tom.selectionEndLine
	startCol := tom.selectionStartCol
	endCol := tom.selectionEndCol
	
	// 规范化选择范围
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	
	if startLine < 0 || endLine >= len(lines) {
		return
	}
	
	var selectedLines []string
	
	if startLine == endLine {
		// 同一行选择
		line := lines[startLine]
		if startCol < len(line) && endCol <= len(line) {
			selectedLines = append(selectedLines, line[startCol:endCol])
		}
	} else {
		// 跨行选择
		// 第一行
		if startLine < len(lines) && startCol < len(lines[startLine]) {
			selectedLines = append(selectedLines, lines[startLine][startCol:])
		}
		
		// 中间行
		for i := startLine + 1; i < endLine; i++ {
			if i < len(lines) {
				selectedLines = append(selectedLines, lines[i])
			}
		}
		
		// 最后一行
		if endLine < len(lines) && endCol <= len(lines[endLine]) {
			selectedLines = append(selectedLines, lines[endLine][:endCol])
		}
	}
	
	tom.selectedText = strings.Join(selectedLines, "\n")
}

// copyToSystemClipboard 复制到系统剪贴板
func (tom *TextOperationsManager) copyToSystemClipboard(text string) {
	// 尝试多种复制方法
	commands := [][]string{
		{"xclip", "-selection", "clipboard"},
		{"xsel", "--clipboard", "--input"},
		{"pbcopy"}, // macOS
	}
	
	for _, cmdArgs := range commands {
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err == nil {
			return // 成功复制
		}
	}
}

// getFromSystemClipboard 从系统剪贴板获取内容
func (tom *TextOperationsManager) getFromSystemClipboard() string {
	// 尝试多种获取方法
	commands := [][]string{
		{"xclip", "-selection", "clipboard", "-o"},
		{"xsel", "--clipboard", "--output"},
		{"pbpaste"}, // macOS
	}
	
	for _, cmdArgs := range commands {
		cmd := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		output, err := cmd.Output()
		if err == nil {
			return string(output)
		}
	}
	
	return ""
}

// truncateText 截断文本用于显示
func (tom *TextOperationsManager) truncateText(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}

// ========== 选择高亮功能 ==========

// IsLineSelected 检查行是否被选中
func (tom *TextOperationsManager) IsLineSelected(lineNum int) bool {
	if !tom.isSelecting {
		return false
	}
	
	startLine := tom.selectionStartLine
	endLine := tom.selectionEndLine
	
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}
	
	return lineNum >= startLine && lineNum <= endLine
}

// GetSelectedText 获取选中的文本
func (tom *TextOperationsManager) GetSelectedText() string {
	return tom.selectedText
}

// HasSelection 检查是否有选中文本
func (tom *TextOperationsManager) HasSelection() bool {
	return tom.selectedText != ""
} 