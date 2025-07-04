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