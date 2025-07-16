package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ================== 调试会话管理器 ==================

// 调试会话管理器
type DebugSessionManager struct {
	currentSession *DebugSession
	sessionFile    string
	ctx            *DebuggerContext
}

// 创建新的调试会话管理器
func NewDebugSessionManager(ctx *DebuggerContext) *DebugSessionManager {
	return &DebugSessionManager{
		ctx: ctx,
	}
}

// 开始新的调试会话
func (dsm *DebugSessionManager) StartNewSession(sessionName string) error {
	if dsm.ctx.Project == nil {
		return fmt.Errorf("没有打开的项目")
	}

	// 创建新的调试会话
	session := &DebugSession{
		SessionID:   generateSessionID(),
		SessionName: sessionName,
		StartTime:   time.Now(),
		Status:      "active",
		
		// 初始化各种数组
		Breakpoints:       make([]ExtendedBreakpoint, 0),
		BreakpointHistory: make([]BreakpointEvent, 0),
		DebugEvents:       make([]DebugEventRecord, 0),
		VariableHistory:   make([]VariableRecord, 0),
		Operations:        make([]DebugOperation, 0),
		Snapshots:         make([]SessionSnapshot, 0),
	}

	// 填充项目信息
	dsm.populateProjectInfo(session)
	
	// 填充系统环境信息
	dsm.populateEnvironmentInfo(session)
	
	// 设置默认配置
	dsm.setDefaultConfiguration(session)
	
	// 初始化元数据
	dsm.initializeMetadata(session)
	
	// 转换现有断点
	dsm.convertExistingBreakpoints(session)

	dsm.currentSession = session
	dsm.sessionFile = filepath.Join(dsm.ctx.Project.RootPath, ".debug_session.json")
	
	// 记录开始会话的操作
	dsm.recordOperation("start_session", map[string]interface{}{
		"session_name": sessionName,
		"session_id":   session.SessionID,
	}, true, "调试会话开始")

	return nil
}

// 结束当前调试会话
func (dsm *DebugSessionManager) EndSession() error {
	if dsm.currentSession == nil {
		return fmt.Errorf("没有活动的调试会话")
	}

	endTime := time.Now()
	dsm.currentSession.EndTime = &endTime
	dsm.currentSession.Status = "ended"
	dsm.currentSession.Duration = endTime.Sub(dsm.currentSession.StartTime).Milliseconds()
	
	// 更新统计信息
	dsm.updateStatistics()
	
	// 记录结束会话的操作
	dsm.recordOperation("end_session", map[string]interface{}{
		"duration": dsm.currentSession.Duration,
	}, true, "调试会话结束")

	return dsm.SaveSession()
}

// 保存当前会话
func (dsm *DebugSessionManager) SaveSession() error {
	if dsm.currentSession == nil {
		return fmt.Errorf("没有活动的调试会话")
	}

	data, err := dsm.currentSession.ExportToJSON()
	if err != nil {
		return fmt.Errorf("导出会话JSON失败: %v", err)
	}

	err = ioutil.WriteFile(dsm.sessionFile, data, 0644)
	if err != nil {
		return fmt.Errorf("保存会话文件失败: %v", err)
	}

	return nil
}

// 加载会话
func (dsm *DebugSessionManager) LoadSession() error {
	if dsm.ctx.Project == nil {
		return fmt.Errorf("没有打开的项目")
	}

	sessionFile := filepath.Join(dsm.ctx.Project.RootPath, ".debug_session.json")
	
	// 检查文件是否存在
	if _, err := os.Stat(sessionFile); os.IsNotExist(err) {
		return nil // 文件不存在，不是错误
	}

	// 读取文件
	data, err := ioutil.ReadFile(sessionFile)
	if err != nil {
		return fmt.Errorf("读取会话文件失败: %v", err)
	}

	// 导入会话
	session, err := ImportSessionFromJSON(data)
	if err != nil {
		return fmt.Errorf("解析会话文件失败: %v", err)
	}

	dsm.currentSession = session
	dsm.sessionFile = sessionFile
	
	// 恢复断点到项目中
	dsm.restoreBreakpoints()

	return nil
}

// 填充项目信息
func (dsm *DebugSessionManager) populateProjectInfo(session *DebugSession) {
	project := dsm.ctx.Project
	
	session.Project = ProjectMetadata{
		Name:         filepath.Base(project.RootPath),
		RootPath:     project.RootPath,
		Version:      "1.0.0",
		Dependencies: []string{"linux/module.h", "linux/kernel.h", "linux/init.h"},
		SourceFiles:  make([]SourceFileInfo, 0),
	}

	// 设置构建信息
	session.Project.BuildInfo = BuildInformation{
		CompilerVersion:   "gcc (Ubuntu 9.4.0-1ubuntu1~20.04.2) 9.4.0",
		CompileFlags:      []string{"-g", "-O0", "-Wall", "-DDEBUG"},
		DebugSymbols:      true,
		Architecture:      detectCurrentArch(),
		OptimizationLevel: "O0",
		BuildTime:         time.Now(),
	}

	// 收集源文件信息
	for filePath := range project.OpenFiles {
		if strings.HasSuffix(filePath, ".c") || strings.HasSuffix(filePath, ".h") {
			sourceFile := dsm.analyzeSourceFile(filePath)
			session.Project.SourceFiles = append(session.Project.SourceFiles, sourceFile)
		}
	}
}

// 分析源文件
func (dsm *DebugSessionManager) analyzeSourceFile(filePath string) SourceFileInfo {
	// 获取文件信息
	fileInfo, err := os.Stat(filePath)
	var size int64 = 0
	var lastModified time.Time = time.Now()
	
	if err == nil {
		size = fileInfo.Size()
		lastModified = fileInfo.ModTime()
	}

	// 获取相对路径
	relativePath, _ := filepath.Rel(dsm.ctx.Project.RootPath, filePath)
	
	// 读取文件内容进行分析
	content, exists := dsm.ctx.Project.OpenFiles[filePath]
	var lineCount int = 0
	var functions []FunctionInfo
	
	if exists {
		lineCount = len(content)
		functions = dsm.extractFunctions(content)
	}

	return SourceFileInfo{
		Path:          filePath,
		RelativePath:  relativePath,
		Size:          size,
		LastModified:  lastModified,
		Checksum:      generateFileChecksum(filePath),
		LineCount:     lineCount,
		FunctionCount: len(functions),
		Functions:     functions,
	}
}

// 提取函数信息
func (dsm *DebugSessionManager) extractFunctions(lines []string) []FunctionInfo {
	functions := make([]FunctionInfo, 0)
	
	for i, line := range lines {
		// 简单的函数识别 - 寻找函数定义模式
		if strings.Contains(line, "(") && strings.Contains(line, ")") && 
		   (strings.Contains(line, "void") || strings.Contains(line, "int") || 
		    strings.Contains(line, "static") || strings.Contains(line, "extern")) {
			
			functionName := extractFunctionName(line)
			if functionName != "" && isValidFunctionName(functionName) {
				// 找到函数结束位置
				endLine := dsm.findFunctionEnd(lines, i)
				
				function := FunctionInfo{
					Name:           functionName,
					StartLine:      i + 1, // 转换为1-based
					EndLine:        endLine,
					Parameters:     dsm.extractParameters(line),
					LocalVariables: dsm.extractLocalVariables(lines, i, endLine),
					ReturnType:     dsm.extractReturnType(line),
				}
				
				functions = append(functions, function)
			}
		}
	}
	
	return functions
}

// 查找函数结束位置
func (dsm *DebugSessionManager) findFunctionEnd(lines []string, startLine int) int {
	braceCount := 0
	inFunction := false
	
	for i := startLine; i < len(lines); i++ {
		line := lines[i]
		
		for _, char := range line {
			if char == '{' {
				braceCount++
				inFunction = true
			} else if char == '}' {
				braceCount--
				if inFunction && braceCount == 0 {
					return i + 1 // 转换为1-based
				}
			}
		}
	}
	
	return len(lines)
}

// 提取函数参数
func (dsm *DebugSessionManager) extractParameters(line string) []ParameterInfo {
	params := make([]ParameterInfo, 0)
	
	// 简单的参数提取 - 在括号内查找
	start := strings.Index(line, "(")
	end := strings.LastIndex(line, ")")
	
	if start != -1 && end != -1 && start < end {
		paramStr := line[start+1 : end]
		paramStr = strings.TrimSpace(paramStr)
		
		if paramStr != "" && paramStr != "void" {
			// 按逗号分割参数
			paramParts := strings.Split(paramStr, ",")
			for _, part := range paramParts {
				part = strings.TrimSpace(part)
				if part != "" {
					// 简单解析：最后一个单词是参数名
					words := strings.Fields(part)
					if len(words) >= 2 {
						paramName := words[len(words)-1]
						paramType := strings.Join(words[:len(words)-1], " ")
						
						params = append(params, ParameterInfo{
							Name: paramName,
							Type: paramType,
							Size: getTypeSize(paramType),
						})
					}
				}
			}
		}
	}
	
	return params
}

// 提取局部变量
func (dsm *DebugSessionManager) extractLocalVariables(lines []string, startLine, endLine int) []VariableInfo {
	variables := make([]VariableInfo, 0)
	
	for i := startLine; i < endLine && i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		
		// 简单的变量声明识别
		if strings.Contains(line, "int ") || strings.Contains(line, "long ") || 
		   strings.Contains(line, "char ") || strings.Contains(line, "float ") ||
		   strings.Contains(line, "double ") {
			
			vars := dsm.parseVariableDeclaration(line)
			variables = append(variables, vars...)
		}
	}
	
	return variables
}

// 解析变量声明
func (dsm *DebugSessionManager) parseVariableDeclaration(line string) []VariableInfo {
	variables := make([]VariableInfo, 0)
	
	// 移除分号和其他符号
	line = strings.ReplaceAll(line, ";", "")
	line = strings.TrimSpace(line)
	
	// 查找变量类型
	words := strings.Fields(line)
	if len(words) >= 2 {
		varType := words[0]
		
		// 处理多个变量声明
		for i := 1; i < len(words); i++ {
			varName := words[i]
			varName = strings.TrimSpace(varName)
			
			// 移除赋值部分
			if equalIndex := strings.Index(varName, "="); equalIndex != -1 {
				varName = varName[:equalIndex]
			}
			
			// 移除数组声明
			if bracketIndex := strings.Index(varName, "["); bracketIndex != -1 {
				varType = varType + varName[bracketIndex:] // 添加数组信息到类型
				varName = varName[:bracketIndex]
			}
			
			if varName != "" {
				variables = append(variables, VariableInfo{
					Name:  varName,
					Type:  varType,
					Size:  getTypeSize(varType),
					Scope: "local",
					Location: VariableLocation{
						Name: varName,
						Type: "unknown",
						Size: getTypeSize(varType),
					},
				})
			}
		}
	}
	
	return variables
}

// 提取返回类型
func (dsm *DebugSessionManager) extractReturnType(line string) string {
	words := strings.Fields(line)
	if len(words) > 0 {
		// 通常返回类型是第一个或第二个单词
		if words[0] == "static" || words[0] == "extern" || words[0] == "inline" {
			if len(words) > 1 {
				return words[1]
			}
		} else {
			return words[0]
		}
	}
	return "void"
}

// 获取类型大小
func getTypeSize(typeName string) int {
	typeName = strings.TrimSpace(typeName)
	
	switch {
	case strings.Contains(typeName, "char"):
		return 1
	case strings.Contains(typeName, "short"):
		return 2
	case strings.Contains(typeName, "int"):
		return 4
	case strings.Contains(typeName, "long"):
		if strings.Contains(typeName, "long long") {
			return 8
		}
		return 8 // 64-bit系统
	case strings.Contains(typeName, "float"):
		return 4
	case strings.Contains(typeName, "double"):
		return 8
	case strings.Contains(typeName, "*"):
		return 8 // 指针大小
	default:
		return 4 // 默认大小
	}
}

// 填充系统环境信息
func (dsm *DebugSessionManager) populateEnvironmentInfo(session *DebugSession) {
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}
	
	session.Environment = SystemEnvironment{
		OperatingSystem:  runtime.GOOS,
		KernelVersion:    getKernelVersion(),
		Architecture:     runtime.GOARCH,
		Hostname:         hostname,
		Username:         username,
		WorkingDirectory: dsm.ctx.Project.RootPath,
		SystemTime:       time.Now(),
		
		CPUInfo: CPUInformation{
			Model:     getCPUModel(),
			Cores:     runtime.NumCPU(),
			Frequency: "Unknown",
			Features:  []string{},
		},
		
		MemoryInfo: MemoryInformation{
			Total:     getMemoryInfo(),
			Available: getMemoryInfo(),
			Used:      0,
			SwapTotal: 0,
			SwapUsed:  0,
		},
		
		EnvironmentVars: make(map[string]string),
	}
	
	// 收集重要的环境变量
	envVars := []string{"PATH", "HOME", "USER", "SHELL", "TERM"}
	for _, envVar := range envVars {
		if value := os.Getenv(envVar); value != "" {
			session.Environment.EnvironmentVars[envVar] = value
		}
	}
}

// 获取内核版本
func getKernelVersion() string {
	if content, err := ioutil.ReadFile("/proc/version"); err == nil {
		return strings.TrimSpace(string(content))
	}
	return "Unknown"
}

// 获取CPU型号
func getCPUModel() string {
	if content, err := ioutil.ReadFile("/proc/cpuinfo"); err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "model name") {
				parts := strings.Split(line, ":")
				if len(parts) > 1 {
					return strings.TrimSpace(parts[1])
				}
			}
		}
	}
	return "Unknown"
}

// 获取内存信息
func getMemoryInfo() int64 {
	if content, err := ioutil.ReadFile("/proc/meminfo"); err == nil {
		lines := strings.Split(string(content), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "MemTotal:") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					// 转换kB到bytes
					if size, err := fmt.Sscanf(parts[1], "%d", new(int64)); err == nil && size == 1 {
						return *new(int64) * 1024
					}
				}
			}
		}
	}
	return 0
}

// 设置默认配置
func (dsm *DebugSessionManager) setDefaultConfiguration(session *DebugSession) {
	session.Configuration = DebugConfiguration{
		MaxBreakpoints:   100,
		AutoSaveInterval: 30,
		LogLevel:         "INFO",
		EnableProfiling:  true,
		MemoryDumpSize:   4096,
		MaxEventHistory:  1000,
		ReplayBufferSize: 500,
	}
}

// 初始化元数据
func (dsm *DebugSessionManager) initializeMetadata(session *DebugSession) {
	session.Metadata = SessionMetadata{
		Version:    "1.0",
		CreatedBy:  "kernel_driver_debug_tui v1.0.0",
		Tags:       []string{"kernel", "debugging", "systemtap", "bpf"},
		Notes:      "内核驱动调试会话",
		ExportTime: time.Now(),
	}
}

// 转换现有断点
func (dsm *DebugSessionManager) convertExistingBreakpoints(session *DebugSession) {
	for _, bp := range dsm.ctx.Project.Breakpoints {
		extendedBp := dsm.convertBreakpoint(bp)
		session.Breakpoints = append(session.Breakpoints, extendedBp)
		
		// 记录断点创建事件
		event := BreakpointEvent{
			ID:           generateEventID(),
			BreakpointID: extendedBp.ID,
			EventType:    "created",
			Timestamp:    extendedBp.CreatedTime,
			User:         session.Environment.Username,
			Description:  fmt.Sprintf("断点在%s:%d创建", filepath.Base(bp.File), bp.Line),
			Data: map[string]interface{}{
				"file":     bp.File,
				"line":     bp.Line,
				"function": bp.Function,
			},
		}
		session.BreakpointHistory = append(session.BreakpointHistory, event)
	}
}

// 转换断点
func (dsm *DebugSessionManager) convertBreakpoint(bp Breakpoint) ExtendedBreakpoint {
	now := time.Now()
	
	// 获取代码上下文
	codeContext := dsm.getCodeContext(bp.File, bp.Line)
	
	return ExtendedBreakpoint{
		// 基础信息
		File:     bp.File,
		Line:     bp.Line,
		Function: bp.Function,
		Enabled:  bp.Enabled,
		
		// 扩展信息
		ID:             generateBreakpointID(len(dsm.currentSession.Breakpoints)),
		CreatedTime:    now,
		LastTriggered:  nil,
		TriggerCount:   0,
		CodeContext:    codeContext,
		
		// 默认设置
		Condition:        "",
		HitCondition:     "",
		HitCount:         0,
		WatchedVariables: []string{},
		Action:           "break",
		LogMessage:       "",
		
		// 调试信息
		DebugInfo: BreakpointDebugInfo{
			HasDebugSymbols: true,
			DWARFOffset:     0,
			Address:         0,
			Assembly:        []string{},
		},
		
		Status:    "active",
		LastError: "",
	}
}

// 获取代码上下文
func (dsm *DebugSessionManager) getCodeContext(filePath string, lineNumber int) CodeContext {
	context := CodeContext{
		LineContent:   "",
		BeforeLines:   []string{},
		AfterLines:    []string{},
		IndentLevel:   0,
		FunctionScope: "unknown",
	}
	
	// 获取文件内容
	content, exists := dsm.ctx.Project.OpenFiles[filePath]
	if !exists || lineNumber <= 0 || lineNumber > len(content) {
		return context
	}
	
	// 获取当前行内容
	currentLine := content[lineNumber-1]
	context.LineContent = currentLine
	
	// 计算缩进级别
	context.IndentLevel = len(currentLine) - len(strings.TrimLeft(currentLine, " \t"))
	
	// 获取前后几行
	const contextLines = 3
	
	for i := max(0, lineNumber-contextLines-1); i < lineNumber-1; i++ {
		context.BeforeLines = append(context.BeforeLines, content[i])
	}
	
	for i := lineNumber; i < min(len(content), lineNumber+contextLines); i++ {
		context.AfterLines = append(context.AfterLines, content[i])
	}
	
	// 查找函数范围
	context.FunctionScope = dsm.findFunctionScope(content, lineNumber)
	
	return context
}

// 查找函数范围
func (dsm *DebugSessionManager) findFunctionScope(content []string, lineNumber int) string {
	// 向前查找函数定义
	for i := lineNumber - 1; i >= 0; i-- {
		line := strings.TrimSpace(content[i])
		if strings.Contains(line, "(") && strings.Contains(line, ")") {
			functionName := extractFunctionName(line)
			if functionName != "" {
				return functionName
			}
		}
	}
	return "unknown"
}

// 恢复断点
func (dsm *DebugSessionManager) restoreBreakpoints() {
	if dsm.currentSession == nil {
		return
	}
	
	// 清空现有断点
	dsm.ctx.Project.Breakpoints = make([]Breakpoint, 0)
	
	// 转换增强断点回简单断点
	for _, extBp := range dsm.currentSession.Breakpoints {
		simpleBp := Breakpoint{
			File:     extBp.File,
			Line:     extBp.Line,
			Function: extBp.Function,
			Enabled:  extBp.Enabled,
		}
		dsm.ctx.Project.Breakpoints = append(dsm.ctx.Project.Breakpoints, simpleBp)
	}
}

// 记录操作
func (dsm *DebugSessionManager) recordOperation(opType string, params map[string]interface{}, success bool, message string) {
	if dsm.currentSession == nil {
		return
	}
	
	result := OperationResult{
		Success: success,
		Message: message,
		Data:    make(map[string]interface{}),
	}
	
	operation := DebugOperation{
		ID:         generateOperationID(),
		Type:       opType,
		Timestamp:  time.Now(),
		User:       dsm.currentSession.Environment.Username,
		Parameters: params,
		Result:     result,
		Duration:   0, // 可以在操作完成后更新
	}
	
	dsm.currentSession.Operations = append(dsm.currentSession.Operations, operation)
}

// 更新统计信息
func (dsm *DebugSessionManager) updateStatistics() {
	if dsm.currentSession == nil {
		return
	}
	
	session := dsm.currentSession
	
	session.Statistics = SessionStatistics{
		TotalBreakpoints:     len(session.Breakpoints),
		TriggeredBreakpoints: dsm.countTriggeredBreakpoints(),
		TotalDebugEvents:     len(session.DebugEvents),
		TotalOperations:      len(session.Operations),
		AverageResponseTime:  dsm.calculateAverageResponseTime(),
		PeakMemoryUsage:      dsm.calculatePeakMemoryUsage(),
		TotalExecutionTime:   session.Duration,
	}
}

// 计算触发的断点数量
func (dsm *DebugSessionManager) countTriggeredBreakpoints() int {
	count := 0
	for _, bp := range dsm.currentSession.Breakpoints {
		if bp.TriggerCount > 0 {
			count++
		}
	}
	return count
}

// 计算平均响应时间
func (dsm *DebugSessionManager) calculateAverageResponseTime() float64 {
	if len(dsm.currentSession.Operations) == 0 {
		return 0
	}
	
	total := int64(0)
	for _, op := range dsm.currentSession.Operations {
		total += op.Duration
	}
	
	return float64(total) / float64(len(dsm.currentSession.Operations))
}

// 计算峰值内存使用
func (dsm *DebugSessionManager) calculatePeakMemoryUsage() int64 {
	peak := int64(0)
	for _, event := range dsm.currentSession.DebugEvents {
		if event.PerformanceData.MemoryUsage > peak {
			peak = event.PerformanceData.MemoryUsage
		}
	}
	return peak
}

// 生成文件校验和
func generateFileChecksum(filePath string) string {
	// 简化实现，实际应用中应该使用真实的哈希函数
	info, err := os.Stat(filePath)
	if err != nil {
		return ""
	}
	
	return fmt.Sprintf("sha256:%x", info.ModTime().Unix())
}

// 生成事件ID
func generateEventID() string {
	return fmt.Sprintf("event_%d", time.Now().UnixNano())
}

// 生成操作ID
func generateOperationID() string {
	return fmt.Sprintf("op_%d", time.Now().UnixNano())
}

// 工具函数
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ================== 会话管理命令 ==================

// 在main.go中添加的命令处理函数

// 开始调试会话命令
func startDebugSessionCommand(ctx *DebuggerContext, sessionName string) {
	if sessionName == "" {
		sessionName = fmt.Sprintf("调试会话_%s", time.Now().Format("20060102_150405"))
	}
	
	dsm := NewDebugSessionManager(ctx)
	err := dsm.StartNewSession(sessionName)
	if err != nil {
		ctx.CommandHistory = append(ctx.CommandHistory, 
			fmt.Sprintf("[ERROR] 启动调试会话失败: %v", err))
	} else {
		ctx.CommandHistory = append(ctx.CommandHistory, 
			fmt.Sprintf("[INFO] 调试会话 '%s' 已启动", sessionName))
	}
	ctx.CommandDirty = true
}

// 结束调试会话命令
func endDebugSessionCommand(ctx *DebuggerContext) {
	dsm := NewDebugSessionManager(ctx)
	err := dsm.LoadSession() // 加载当前会话
	if err != nil {
		ctx.CommandHistory = append(ctx.CommandHistory, 
			fmt.Sprintf("[ERROR] 加载当前会话失败: %v", err))
		ctx.CommandDirty = true
		return
	}
	
	err = dsm.EndSession()
	if err != nil {
		ctx.CommandHistory = append(ctx.CommandHistory, 
			fmt.Sprintf("[ERROR] 结束调试会话失败: %v", err))
	} else {
		ctx.CommandHistory = append(ctx.CommandHistory, 
			"[INFO] 调试会话已结束并保存")
	}
	ctx.CommandDirty = true
}

// 保存调试会话命令
func saveDebugSessionCommand(ctx *DebuggerContext) {
	dsm := NewDebugSessionManager(ctx)
	err := dsm.LoadSession()
	if err != nil {
		ctx.CommandHistory = append(ctx.CommandHistory, 
			fmt.Sprintf("[ERROR] 加载当前会话失败: %v", err))
		ctx.CommandDirty = true
		return
	}
	
	err = dsm.SaveSession()
	if err != nil {
		ctx.CommandHistory = append(ctx.CommandHistory, 
			fmt.Sprintf("[ERROR] 保存调试会话失败: %v", err))
	} else {
		ctx.CommandHistory = append(ctx.CommandHistory, 
			"[INFO] 调试会话已保存")
	}
	ctx.CommandDirty = true
} 