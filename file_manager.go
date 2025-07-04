package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"io/ioutil"
	"sort"
	"strconv"
)

// ========== 文件和项目管理器 ==========

type FileManager struct {
	ctx *DebuggerContext
}

// NewFileManager 创建文件管理器
func NewFileManager(ctx *DebuggerContext) *FileManager {
	return &FileManager{ctx: ctx}
}

// ========== 项目初始化 ==========

// InitProject 初始化项目
func (fm *FileManager) InitProject(rootPath string) error {
	// 检查路径是否存在
	info, err := os.Stat(rootPath)
	if err != nil {
		return fmt.Errorf("路径不存在: %v", err)
	}
	
	if !info.IsDir() {
		return fmt.Errorf("路径不是目录: %s", rootPath)
	}
	
	// 获取绝对路径
	absPath, err := filepath.Abs(rootPath)
	if err != nil {
		return fmt.Errorf("获取绝对路径失败: %v", err)
	}
	
	// 创建项目信息
	fm.ctx.Project = &ProjectInfo{
		RootPath:    absPath,
		OpenFiles:   make(map[string][]string),
		CurrentFile: "",
		Breakpoints: make([]Breakpoint, 0),
	}
	
	// 构建文件树
	err = fm.BuildFileTree()
	if err != nil {
		return fmt.Errorf("构建文件树失败: %v", err)
	}
	
	return nil
}

// ========== 文件树操作 ==========

// BuildFileTree 构建文件树
func (fm *FileManager) BuildFileTree() error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	root, err := fm.buildFileNode(fm.ctx.Project.RootPath, true)
	if err != nil {
		return err
	}
	
	fm.ctx.Project.FileTree = root
	return nil
}

// buildFileNode 递归构建文件节点
func (fm *FileManager) buildFileNode(path string, isRoot bool) (*FileNode, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	
	node := &FileNode{
		Name:     filepath.Base(path),
		Path:     path,
		IsDir:    info.IsDir(),
		Children: nil,
		Expanded: isRoot, // 根目录默认展开
	}
	
	// 如果是根目录，使用项目名作为显示名称
	if isRoot {
		node.Name = filepath.Base(path)
	}
	
	// 如果是目录，递归读取子项
	if info.IsDir() {
		entries, err := ioutil.ReadDir(path)
		if err != nil {
			return node, nil // 返回节点但不包含子项
		}
		
		var children []*FileNode
		for _, entry := range entries {
			// 跳过隐藏文件和某些目录
			if fm.shouldSkipFile(entry.Name()) {
				continue
			}
			
			childPath := filepath.Join(path, entry.Name())
			childNode, err := fm.buildFileNode(childPath, false)
			if err != nil {
				continue // 跳过无法访问的文件
			}
			
			children = append(children, childNode)
		}
		
		// 排序：目录在前，文件在后，按字母顺序
		sort.Slice(children, func(i, j int) bool {
			if children[i].IsDir != children[j].IsDir {
				return children[i].IsDir
			}
			return children[i].Name < children[j].Name
		})
		
		node.Children = children
	}
	
	return node, nil
}

// shouldSkipFile 判断是否应该跳过某个文件
func (fm *FileManager) shouldSkipFile(name string) bool {
	// 跳过的文件和目录
	skipList := []string{
		".", "..", ".git", ".svn", ".hg",
		"node_modules", "vendor", ".vscode",
		".idea", "build", "dist", "target",
		"*.o", "*.so", "*.a", "*.out",
	}
	
	for _, skip := range skipList {
		if strings.HasPrefix(name, ".") && name != "." && name != ".." {
			return true
		}
		if name == skip {
			return true
		}
	}
	
	return false
}

// ToggleFileExpansion 切换文件夹展开状态
func (fm *FileManager) ToggleFileExpansion(path string) error {
	if fm.ctx.Project == nil || fm.ctx.Project.FileTree == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	node := fm.findFileNode(fm.ctx.Project.FileTree, path)
	if node == nil {
		return fmt.Errorf("文件节点未找到: %s", path)
	}
	
	if node.IsDir {
		node.Expanded = !node.Expanded
	}
	
	return nil
}

// findFileNode 查找文件节点
func (fm *FileManager) findFileNode(root *FileNode, path string) *FileNode {
	if root.Path == path {
		return root
	}
	
	if root.Children != nil {
		for _, child := range root.Children {
			if result := fm.findFileNode(child, path); result != nil {
				return result
			}
		}
	}
	
	return nil
}

// ========== 文件操作 ==========

// OpenFile 打开文件并读取内容
func (fm *FileManager) OpenFile(filePath string) error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	// 检查文件是否存在
	if _, err := os.Stat(filePath); err != nil {
		return fmt.Errorf("文件不存在: %v", err)
	}
	
	// 读取文件内容
	content, err := ioutil.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("读取文件失败: %v", err)
	}
	
	// 将内容按行分割
	lines := strings.Split(string(content), "\n")
	
	// 保存到项目中
	fm.ctx.Project.OpenFiles[filePath] = lines
	fm.ctx.Project.CurrentFile = filePath
	
	return nil
}

// GetFileContent 获取已打开文件的内容
func (fm *FileManager) GetFileContent(filePath string) ([]string, error) {
	if fm.ctx.Project == nil {
		return nil, fmt.Errorf("项目未初始化")
	}
	
	if content, exists := fm.ctx.Project.OpenFiles[filePath]; exists {
		return content, nil
	}
	
	// 如果文件未打开，尝试打开它
	err := fm.OpenFile(filePath)
	if err != nil {
		return nil, err
	}
	
	return fm.ctx.Project.OpenFiles[filePath], nil
}

// GetCurrentFileContent 获取当前打开文件的内容
func (fm *FileManager) GetCurrentFileContent() ([]string, error) {
	if fm.ctx.Project == nil || fm.ctx.Project.CurrentFile == "" {
		return nil, fmt.Errorf("没有打开的文件")
	}
	
	return fm.GetFileContent(fm.ctx.Project.CurrentFile)
}

// ListOpenFiles 列出所有已打开的文件
func (fm *FileManager) ListOpenFiles() []string {
	if fm.ctx.Project == nil {
		return []string{}
	}
	
	var files []string
	for filePath := range fm.ctx.Project.OpenFiles {
		files = append(files, filePath)
	}
	
	sort.Strings(files)
	return files
}

// CloseFile 关闭文件
func (fm *FileManager) CloseFile(filePath string) {
	if fm.ctx.Project == nil {
		return
	}
	
	delete(fm.ctx.Project.OpenFiles, filePath)
	
	// 如果关闭的是当前文件，清空当前文件
	if fm.ctx.Project.CurrentFile == filePath {
		fm.ctx.Project.CurrentFile = ""
		
		// 如果还有其他打开的文件，选择一个作为当前文件
		openFiles := fm.ListOpenFiles()
		if len(openFiles) > 0 {
			fm.ctx.Project.CurrentFile = openFiles[0]
		}
	}
}

// ========== 断点管理 ==========

// AddBreakpoint 添加断点
func (fm *FileManager) AddBreakpoint(file string, line int, function string) error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	// 检查断点是否已存在
	for _, bp := range fm.ctx.Project.Breakpoints {
		if bp.File == file && bp.Line == line {
			return fmt.Errorf("断点已存在于 %s:%d", file, line)
		}
	}
	
	// 添加新断点
	breakpoint := Breakpoint{
		File:     file,
		Line:     line,
		Function: function,
		Enabled:  true,
	}
	
	fm.ctx.Project.Breakpoints = append(fm.ctx.Project.Breakpoints, breakpoint)
	
	return nil
}

// RemoveBreakpoint 移除断点
func (fm *FileManager) RemoveBreakpoint(file string, line int) error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	for i, bp := range fm.ctx.Project.Breakpoints {
		if bp.File == file && bp.Line == line {
			// 移除断点
			fm.ctx.Project.Breakpoints = append(
				fm.ctx.Project.Breakpoints[:i],
				fm.ctx.Project.Breakpoints[i+1:]...)
			return nil
		}
	}
	
	return fmt.Errorf("断点不存在于 %s:%d", file, line)
}

// ToggleBreakpoint 切换断点启用状态
func (fm *FileManager) ToggleBreakpoint(file string, line int) error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	for i, bp := range fm.ctx.Project.Breakpoints {
		if bp.File == file && bp.Line == line {
			fm.ctx.Project.Breakpoints[i].Enabled = !bp.Enabled
			return nil
		}
	}
	
	return fmt.Errorf("断点不存在于 %s:%d", file, line)
}

// GetBreakpoints 获取所有断点
func (fm *FileManager) GetBreakpoints() []Breakpoint {
	if fm.ctx.Project == nil {
		return []Breakpoint{}
	}
	
	return fm.ctx.Project.Breakpoints
}

// GetBreakpointsForFile 获取指定文件的断点
func (fm *FileManager) GetBreakpointsForFile(file string) []Breakpoint {
	if fm.ctx.Project == nil {
		return []Breakpoint{}
	}
	
	var fileBreakpoints []Breakpoint
	for _, bp := range fm.ctx.Project.Breakpoints {
		if bp.File == file {
			fileBreakpoints = append(fileBreakpoints, bp)
		}
	}
	
	return fileBreakpoints
}

// HasBreakpoint 检查指定位置是否有断点
func (fm *FileManager) HasBreakpoint(file string, line int) bool {
	if fm.ctx.Project == nil {
		return false
	}
	
	for _, bp := range fm.ctx.Project.Breakpoints {
		if bp.File == file && bp.Line == line {
			return true
		}
	}
	
	return false
}

// ========== 代码分析 ==========

// AnalyzeCurrentFile 分析当前文件
func (fm *FileManager) AnalyzeCurrentFile() map[string]interface{} {
	result := make(map[string]interface{})
	
	if fm.ctx.Project == nil || fm.ctx.Project.CurrentFile == "" {
		result["error"] = "没有当前文件"
		return result
	}
	
	filePath := fm.ctx.Project.CurrentFile
	content, err := fm.GetCurrentFileContent()
	if err != nil {
		result["error"] = err.Error()
		return result
	}
	
	// 基本信息
	result["file"] = filePath
	result["lines"] = len(content)
	result["size"] = len(strings.Join(content, "\n"))
	
	// 代码分析
	functions := fm.extractFunctions(content)
	result["functions"] = functions
	result["function_count"] = len(functions)
	
	// 断点信息
	breakpoints := fm.GetBreakpointsForFile(filePath)
	result["breakpoints"] = len(breakpoints)
	
	return result
}

// extractFunctions 提取函数定义（简单实现）
func (fm *FileManager) extractFunctions(content []string) []string {
	var functions []string
	
	for _, line := range content {
		line = strings.TrimSpace(line)
		
		// 简单的函数检测（C/C++风格）
		if strings.Contains(line, "(") && strings.Contains(line, ")") && 
		   !strings.HasPrefix(line, "//") && !strings.HasPrefix(line, "#") &&
		   !strings.Contains(line, "=") {
			
			// 提取函数名
			parts := strings.Split(line, "(")
			if len(parts) >= 2 {
				funcPart := strings.TrimSpace(parts[0])
				words := strings.Fields(funcPart)
				if len(words) > 0 {
					funcName := words[len(words)-1]
					if funcName != "" && !strings.Contains(funcName, "*") {
						functions = append(functions, funcName)
					}
				}
			}
		}
	}
	
	return functions
}

// ========== 快速断点操作 ==========

// AddBreakpointByFunction 根据函数名添加断点
func (fm *FileManager) AddBreakpointByFunction(functionName string) error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	// 在所有打开的文件中搜索函数
	var foundFile string
	var foundLine int
	
	for filePath, content := range fm.ctx.Project.OpenFiles {
		for i, line := range content {
			if strings.Contains(line, functionName+"(") {
				foundFile = filePath
				foundLine = i + 1 // 行号从1开始
				break
			}
		}
		if foundFile != "" {
			break
		}
	}
	
	if foundFile == "" {
		return fmt.Errorf("函数 %s 未找到", functionName)
	}
	
	return fm.AddBreakpoint(foundFile, foundLine, functionName)
}

// AddBreakpointAtLine 在当前文件的指定行添加断点
func (fm *FileManager) AddBreakpointAtLine(lineStr string) error {
	if fm.ctx.Project == nil || fm.ctx.Project.CurrentFile == "" {
		return fmt.Errorf("没有打开的文件")
	}
	
	line, err := strconv.Atoi(lineStr)
	if err != nil {
		return fmt.Errorf("无效的行号: %s", lineStr)
	}
	
	content, err := fm.GetCurrentFileContent()
	if err != nil {
		return err
	}
	
	if line < 1 || line > len(content) {
		return fmt.Errorf("行号超出范围: %d (文件共 %d 行)", line, len(content))
	}
	
	// 尝试从代码行提取函数名
	functionName := fm.guessFunctionName(content, line-1)
	
	return fm.AddBreakpoint(fm.ctx.Project.CurrentFile, line, functionName)
}

// guessFunctionName 猜测指定行的函数名
func (fm *FileManager) guessFunctionName(content []string, lineIndex int) string {
	// 向上查找最近的函数定义
	for i := lineIndex; i >= 0; i-- {
		line := strings.TrimSpace(content[i])
		if strings.Contains(line, "(") && strings.Contains(line, ")") {
			parts := strings.Split(line, "(")
			if len(parts) >= 2 {
				funcPart := strings.TrimSpace(parts[0])
				words := strings.Fields(funcPart)
				if len(words) > 0 {
					return words[len(words)-1]
				}
			}
		}
	}
	
	return "unknown_function"
}

// ========== 文件搜索功能 ==========

// SearchInFiles 在文件中搜索文本
func (fm *FileManager) SearchInFiles(searchTerm string, searchInAllFiles bool) (map[string][]SearchResult, error) {
	results := make(map[string][]SearchResult)
	
	if fm.ctx.Project == nil {
		return results, fmt.Errorf("项目未初始化")
	}
	
	if searchInAllFiles {
		// 在项目所有文件中搜索
		return fm.searchInAllProjectFiles(searchTerm), nil
	} else {
		// 仅在当前文件中搜索
		if fm.ctx.Project.CurrentFile == "" {
			return results, fmt.Errorf("没有打开的文件")
		}
		
		fileResults := fm.searchInSingleFile(fm.ctx.Project.CurrentFile, searchTerm)
		if len(fileResults) > 0 {
			results[fm.ctx.Project.CurrentFile] = fileResults
		}
		
		return results, nil
	}
}

// searchInAllProjectFiles 在所有项目文件中搜索
func (fm *FileManager) searchInAllProjectFiles(searchTerm string) map[string][]SearchResult {
	results := make(map[string][]SearchResult)
	
	if fm.ctx.Project.FileTree == nil {
		return results
	}
	
	fm.searchInFileTree(fm.ctx.Project.FileTree, searchTerm, results)
	return results
}

// searchInFileTree 递归搜索文件树
func (fm *FileManager) searchInFileTree(node *FileNode, searchTerm string, results map[string][]SearchResult) {
	if node == nil {
		return
	}
	
	if node.IsDir {
		for _, child := range node.Children {
			fm.searchInFileTree(child, searchTerm, results)
		}
	} else if fm.isSearchableFile(node.Name) {
		fileResults := fm.searchInSingleFile(node.Path, searchTerm)
		if len(fileResults) > 0 {
			results[node.Path] = fileResults
		}
	}
}

// searchInSingleFile 在单个文件中搜索
func (fm *FileManager) searchInSingleFile(filePath, searchTerm string) []SearchResult {
	var results []SearchResult
	
	content, err := fm.GetFileContent(filePath)
	if err != nil {
		return results
	}
	
	for lineIndex, line := range content {
		lineNum := lineIndex + 1
		lowerLine := strings.ToLower(line)
		lowerTerm := strings.ToLower(searchTerm)
		
		startIndex := 0
		for {
			index := strings.Index(lowerLine[startIndex:], lowerTerm)
			if index == -1 {
				break
			}
			
			actualIndex := startIndex + index
			result := SearchResult{
				LineNumber:  lineNum,
				StartColumn: actualIndex,
				EndColumn:   actualIndex + len(searchTerm),
				Text:        line[actualIndex:actualIndex+len(searchTerm)],
			}
			
			results = append(results, result)
			startIndex = actualIndex + 1
		}
	}
	
	return results
}

// isSearchableFile 检查文件是否可搜索
func (fm *FileManager) isSearchableFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	searchableExts := []string{".c", ".cpp", ".h", ".hpp", ".go", ".py", ".js", ".ts", ".java", ".txt", ".md", ".json", ".xml"}
	
	for _, searchableExt := range searchableExts {
		if ext == searchableExt {
			return true
		}
	}
	
	return false
}

// ========== 文件操作扩展 ==========

// SaveFile 保存文件
func (fm *FileManager) SaveFile(filePath string, content []string) error {
	if filePath == "" {
		return fmt.Errorf("文件路径为空")
	}
	
	// 创建目录（如果不存在）
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %v", err)
	}
	
	// 写入文件
	fileContent := strings.Join(content, "\n")
	if err := ioutil.WriteFile(filePath, []byte(fileContent), 0644); err != nil {
		return fmt.Errorf("写入文件失败: %v", err)
	}
	
	// 更新缓存
	if fm.ctx.Project != nil {
		fm.ctx.Project.OpenFiles[filePath] = content
		
		// 从修改列表中移除
		modifiedFiles := []string{}
		for _, modFile := range fm.ctx.Project.ModifiedFiles {
			if modFile != filePath {
				modifiedFiles = append(modifiedFiles, modFile)
			}
		}
		fm.ctx.Project.ModifiedFiles = modifiedFiles
	}
	
	return nil
}

// SaveCurrentFile 保存当前文件
func (fm *FileManager) SaveCurrentFile() error {
	if fm.ctx.Project == nil || fm.ctx.Project.CurrentFile == "" {
		return fmt.Errorf("没有当前文件")
	}
	
	content, exists := fm.ctx.Project.OpenFiles[fm.ctx.Project.CurrentFile]
	if !exists {
		return fmt.Errorf("文件内容未加载")
	}
	
	return fm.SaveFile(fm.ctx.Project.CurrentFile, content)
}

// ReloadFile 重新加载文件
func (fm *FileManager) ReloadFile(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("文件路径为空")
	}
	
	// 从磁盘重新读取
	content, err := fm.GetFileContent(filePath)
	if err != nil {
		return err
	}
	
	// 更新缓存
	if fm.ctx.Project != nil {
		fm.ctx.Project.OpenFiles[filePath] = content
		
		// 从修改列表中移除
		modifiedFiles := []string{}
		for _, modFile := range fm.ctx.Project.ModifiedFiles {
			if modFile != filePath {
				modifiedFiles = append(modifiedFiles, modFile)
			}
		}
		fm.ctx.Project.ModifiedFiles = modifiedFiles
	}
	
	return nil
}

// IsFileModified 检查文件是否已修改
func (fm *FileManager) IsFileModified(filePath string) bool {
	if fm.ctx.Project == nil {
		return false
	}
	
	for _, modFile := range fm.ctx.Project.ModifiedFiles {
		if modFile == filePath {
			return true
		}
	}
	
	return false
}

// GetModifiedFiles 获取所有修改的文件
func (fm *FileManager) GetModifiedFiles() []string {
	if fm.ctx.Project == nil {
		return []string{}
	}
	
	return fm.ctx.Project.ModifiedFiles
}

// ========== 断点管理扩展 ==========

// ClearAllBreakpoints 清除所有断点
func (fm *FileManager) ClearAllBreakpoints() error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	count := len(fm.ctx.Project.Breakpoints)
	fm.ctx.Project.Breakpoints = []Breakpoint{}
	
	fm.ctx.CommandHistory = append(fm.ctx.CommandHistory, 
		fmt.Sprintf("清除了 %d 个断点", count))
	fm.ctx.CommandDirty = true
	
	return nil
}

// DisableAllBreakpoints 禁用所有断点
func (fm *FileManager) DisableAllBreakpoints() error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	count := 0
	for i := range fm.ctx.Project.Breakpoints {
		if fm.ctx.Project.Breakpoints[i].Enabled {
			fm.ctx.Project.Breakpoints[i].Enabled = false
			count++
		}
	}
	
	fm.ctx.CommandHistory = append(fm.ctx.CommandHistory, 
		fmt.Sprintf("禁用了 %d 个断点", count))
	fm.ctx.CommandDirty = true
	
	return nil
}

// EnableAllBreakpoints 启用所有断点
func (fm *FileManager) EnableAllBreakpoints() error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	count := 0
	for i := range fm.ctx.Project.Breakpoints {
		if !fm.ctx.Project.Breakpoints[i].Enabled {
			fm.ctx.Project.Breakpoints[i].Enabled = true
			count++
		}
	}
	
	fm.ctx.CommandHistory = append(fm.ctx.CommandHistory, 
		fmt.Sprintf("启用了 %d 个断点", count))
	fm.ctx.CommandDirty = true
	
	return nil
}

// ExportBreakpoints 导出断点配置
func (fm *FileManager) ExportBreakpoints() (string, error) {
	if fm.ctx.Project == nil {
		return "", fmt.Errorf("项目未初始化")
	}
	
	var lines []string
	lines = append(lines, "# Breakpoints Configuration")
	lines = append(lines, "# Format: file:line:function:enabled")
	lines = append(lines, "")
	
	for _, bp := range fm.ctx.Project.Breakpoints {
		enabled := "disabled"
		if bp.Enabled {
			enabled = "enabled"
		}
		
		line := fmt.Sprintf("%s:%d:%s:%s", bp.File, bp.Line, bp.Function, enabled)
		lines = append(lines, line)
	}
	
	return strings.Join(lines, "\n"), nil
}

// ImportBreakpoints 导入断点配置
func (fm *FileManager) ImportBreakpoints(configContent string) error {
	if fm.ctx.Project == nil {
		return fmt.Errorf("项目未初始化")
	}
	
	lines := strings.Split(configContent, "\n")
	imported := 0
	
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		
		parts := strings.Split(line, ":")
		if len(parts) != 4 {
			continue
		}
		
		file := parts[0]
		lineNum, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		
		function := parts[2]
		enabled := parts[3] == "enabled"
		
		// 添加断点
		breakpoint := Breakpoint{
			File:     file,
			Line:     lineNum,
			Function: function,
			Enabled:  enabled,
		}
		
		fm.ctx.Project.Breakpoints = append(fm.ctx.Project.Breakpoints, breakpoint)
		imported++
	}
	
	fm.ctx.CommandHistory = append(fm.ctx.CommandHistory, 
		fmt.Sprintf("导入了 %d 个断点", imported))
	fm.ctx.CommandDirty = true
	
	return nil
}

// ========== 文件统计和分析 ==========

// GetProjectStats 获取项目统计信息
func (fm *FileManager) GetProjectStats() map[string]interface{} {
	stats := make(map[string]interface{})
	
	if fm.ctx.Project == nil {
		stats["error"] = "项目未初始化"
		return stats
	}
	
	stats["project_path"] = fm.ctx.Project.RootPath
	stats["open_files"] = len(fm.ctx.Project.OpenFiles)
	stats["modified_files"] = len(fm.ctx.Project.ModifiedFiles)
	stats["breakpoints"] = len(fm.ctx.Project.Breakpoints)
	
	// 统计文件类型
	fileTypes := make(map[string]int)
	totalLines := 0
	totalSize := 0
	
	for filePath, content := range fm.ctx.Project.OpenFiles {
		ext := strings.ToLower(filepath.Ext(filePath))
		if ext == "" {
			ext = "none"
		}
		fileTypes[ext]++
		
		totalLines += len(content)
		totalSize += len(strings.Join(content, "\n"))
	}
	
	stats["file_types"] = fileTypes
	stats["total_lines"] = totalLines
	stats["total_size"] = totalSize
	
	// 当前文件信息
	if fm.ctx.Project.CurrentFile != "" {
		stats["current_file"] = fm.ctx.Project.CurrentFile
		if content, exists := fm.ctx.Project.OpenFiles[fm.ctx.Project.CurrentFile]; exists {
			stats["current_file_lines"] = len(content)
		}
	}
	
	return stats
}

// GetFileHistory 获取文件访问历史（模拟）
func (fm *FileManager) GetFileHistory() []string {
	// 在实际实现中，这里会维护一个文件访问历史
	// 现在返回已打开的文件作为历史
	if fm.ctx.Project == nil {
		return []string{}
	}
	
	var history []string
	for filePath := range fm.ctx.Project.OpenFiles {
		history = append(history, filePath)
	}
	
	return history
}