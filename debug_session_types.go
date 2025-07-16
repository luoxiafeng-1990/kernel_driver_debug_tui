package main

import (
	"time"
	"encoding/json"
	"fmt"
)

// ================== 增强版调试会话JSON结构 ==================

// 调试会话信息 - 顶层结构
type DebugSession struct {
	// 会话基础信息
	SessionID          string            `json:"session_id"`          // 唯一会话ID
	SessionName        string            `json:"session_name"`        // 会话名称
	StartTime          time.Time         `json:"start_time"`          // 会话开始时间
	EndTime            *time.Time        `json:"end_time,omitempty"`  // 会话结束时间（可空）
	Duration           int64             `json:"duration"`            // 会话持续时间（毫秒）
	Status             string            `json:"status"`              // "active", "paused", "ended", "error"
	
	// 项目信息
	Project            ProjectMetadata   `json:"project"`             // 项目元数据
	
	// 系统环境信息
	Environment        SystemEnvironment `json:"environment"`         // 系统环境
	
	// 断点管理
	Breakpoints        []ExtendedBreakpoint `json:"breakpoints"`      // 增强版断点列表
	BreakpointHistory  []BreakpointEvent    `json:"breakpoint_history"` // 断点操作历史
	
	// 调试事件记录
	DebugEvents        []DebugEventRecord   `json:"debug_events"`     // 调试事件记录
	VariableHistory    []VariableRecord     `json:"variable_history"` // 变量监控历史
	
	// 重放支持
	Operations         []DebugOperation     `json:"operations"`       // 操作序列（支持重放）
	Snapshots          []SessionSnapshot    `json:"snapshots"`        // 会话快照
	
	// 统计信息
	Statistics         SessionStatistics    `json:"statistics"`       // 会话统计
	
	// 配置信息
	Configuration      DebugConfiguration   `json:"configuration"`    // 调试配置
	
	// 元数据
	Metadata           SessionMetadata      `json:"metadata"`         // 额外元数据
}

// 项目元数据
type ProjectMetadata struct {
	Name               string            `json:"name"`               // 项目名称
	RootPath           string            `json:"root_path"`          // 项目根目录
	Version            string            `json:"version"`            // 项目版本
	BuildInfo          BuildInformation  `json:"build_info"`         // 构建信息
	SourceFiles        []SourceFileInfo  `json:"source_files"`       // 源文件信息
	Dependencies       []string          `json:"dependencies"`       // 依赖项列表
}

// 构建信息
type BuildInformation struct {
	CompilerVersion    string            `json:"compiler_version"`   // 编译器版本
	CompileFlags       []string          `json:"compile_flags"`      // 编译选项
	DebugSymbols       bool              `json:"debug_symbols"`      // 是否包含调试符号
	Architecture       string            `json:"architecture"`       // 目标架构
	OptimizationLevel  string            `json:"optimization_level"` // 优化级别
	BuildTime          time.Time         `json:"build_time"`         // 构建时间
}

// 源文件信息
type SourceFileInfo struct {
	Path               string            `json:"path"`               // 文件路径
	RelativePath       string            `json:"relative_path"`      // 相对路径
	Size               int64             `json:"size"`               // 文件大小
	LastModified       time.Time         `json:"last_modified"`      // 最后修改时间
	Checksum           string            `json:"checksum"`           // 文件校验和
	LineCount          int               `json:"line_count"`         // 行数
	FunctionCount      int               `json:"function_count"`     // 函数数量
	Functions          []FunctionInfo    `json:"functions"`          // 函数列表
}

// 函数信息
type FunctionInfo struct {
	Name               string            `json:"name"`               // 函数名
	StartLine          int               `json:"start_line"`         // 开始行号
	EndLine            int               `json:"end_line"`           // 结束行号
	Parameters         []ParameterInfo   `json:"parameters"`         // 参数列表
	LocalVariables     []VariableInfo    `json:"local_variables"`    // 局部变量
	ReturnType         string            `json:"return_type"`        // 返回类型
}

// 参数信息
type ParameterInfo struct {
	Name               string            `json:"name"`               // 参数名
	Type               string            `json:"type"`               // 参数类型
	Size               int               `json:"size"`               // 大小（字节）
}

// 变量信息
type VariableInfo struct {
	Name               string            `json:"name"`               // 变量名
	Type               string            `json:"type"`               // 变量类型
	Size               int               `json:"size"`               // 大小（字节）
	Scope              string            `json:"scope"`              // 作用域 "local", "global", "parameter"
	Location           VariableLocation  `json:"location"`           // 变量位置信息
}

// 系统环境信息
type SystemEnvironment struct {
	OperatingSystem    string            `json:"operating_system"`   // 操作系统
	KernelVersion      string            `json:"kernel_version"`     // 内核版本
	Architecture       string            `json:"architecture"`       // 系统架构
	CPUInfo            CPUInformation    `json:"cpu_info"`           // CPU信息
	MemoryInfo         MemoryInformation `json:"memory_info"`        // 内存信息
	Hostname           string            `json:"hostname"`           // 主机名
	Username           string            `json:"username"`           // 用户名
	WorkingDirectory   string            `json:"working_directory"`  // 工作目录
	EnvironmentVars    map[string]string `json:"environment_vars"`   // 环境变量
	SystemTime         time.Time         `json:"system_time"`        // 系统时间
}

// CPU信息
type CPUInformation struct {
	Model              string            `json:"model"`              // CPU型号
	Cores              int               `json:"cores"`              // 核心数
	Frequency          string            `json:"frequency"`          // 频率
	Features           []string          `json:"features"`           // CPU特性
}

// 内存信息
type MemoryInformation struct {
	Total              int64             `json:"total"`              // 总内存（字节）
	Available          int64             `json:"available"`          // 可用内存（字节）
	Used               int64             `json:"used"`               // 已用内存（字节）
	SwapTotal          int64             `json:"swap_total"`         // 交换内存总量
	SwapUsed           int64             `json:"swap_used"`          // 交换内存使用量
}

// 增强版断点信息
type ExtendedBreakpoint struct {
	// 基础断点信息（保持兼容）
	File               string            `json:"file"`               // 文件路径
	Line               int               `json:"line"`               // 行号
	Function           string            `json:"function"`           // 函数名
	Enabled            bool              `json:"enabled"`            // 是否启用
	
	// 扩展信息
	ID                 string            `json:"id"`                 // 断点唯一ID
	CreatedTime        time.Time         `json:"created_time"`       // 创建时间
	LastTriggered      *time.Time        `json:"last_triggered,omitempty"` // 最后触发时间
	TriggerCount       int               `json:"trigger_count"`      // 触发次数
	
	// 代码上下文
	CodeContext        CodeContext       `json:"code_context"`       // 代码上下文
	
	// 条件断点支持
	Condition          string            `json:"condition"`          // 断点条件表达式
	HitCondition       string            `json:"hit_condition"`      // 命中条件 ">=", "==", "%"
	HitCount           int               `json:"hit_count"`          // 命中计数器
	
	// 变量监控
	WatchedVariables   []string          `json:"watched_variables"`  // 监控的变量列表
	
	// 断点行为
	Action             string            `json:"action"`             // "break", "log", "trace"
	LogMessage         string            `json:"log_message"`        // 日志消息模板
	
	// 调试信息
	DebugInfo          BreakpointDebugInfo `json:"debug_info"`       // 调试符号信息
	
	// 状态信息
	Status             string            `json:"status"`             // "active", "disabled", "invalid"
	LastError          string            `json:"last_error"`         // 最后错误信息
}

// 代码上下文
type CodeContext struct {
	LineContent        string            `json:"line_content"`       // 断点行内容
	BeforeLines        []string          `json:"before_lines"`       // 前几行代码
	AfterLines         []string          `json:"after_lines"`        // 后几行代码
	IndentLevel        int               `json:"indent_level"`       // 缩进级别
	FunctionScope      string            `json:"function_scope"`     // 所在函数范围
}

// 断点调试信息
type BreakpointDebugInfo struct {
	HasDebugSymbols    bool              `json:"has_debug_symbols"`  // 是否有调试符号
	DWARFOffset        uint64            `json:"dwarf_offset"`       // DWARF偏移
	Address            uint64            `json:"address"`            // 内存地址
	Assembly           []string          `json:"assembly"`           // 汇编代码
}

// 断点事件（断点操作历史）
type BreakpointEvent struct {
	ID                 string            `json:"id"`                 // 事件ID
	BreakpointID       string            `json:"breakpoint_id"`      // 断点ID
	EventType          string            `json:"event_type"`         // "created", "enabled", "disabled", "modified", "deleted", "triggered"
	Timestamp          time.Time         `json:"timestamp"`          // 时间戳
	User               string            `json:"user"`               // 操作用户
	Description        string            `json:"description"`        // 事件描述
	Data               map[string]interface{} `json:"data"`          // 附加数据
}

// 调试事件记录（断点触发记录）
type DebugEventRecord struct {
	ID                 string            `json:"id"`                 // 事件ID
	BreakpointID       string            `json:"breakpoint_id"`      // 断点ID
	Timestamp          time.Time         `json:"timestamp"`          // 触发时间
	
	// 进程信息
	ProcessInfo        ProcessInformation `json:"process_info"`      // 进程信息
	
	// 执行上下文
	ExecutionContext   ExecutionContext  `json:"execution_context"`  // 执行上下文
	
	// 变量状态
	VariableSnapshots  []VariableSnapshot `json:"variable_snapshots"` // 变量快照
	
	// 调用栈
	CallStack          []StackFrame      `json:"call_stack"`         // 调用栈
	
	// 寄存器状态
	RegisterState      map[string]interface{} `json:"register_state"` // 寄存器状态
	
	// 内存状态
	MemorySnapshots    []MemorySnapshot  `json:"memory_snapshots"`   // 内存快照
	
	// 性能信息
	PerformanceData    PerformanceInfo   `json:"performance_data"`   // 性能数据
}

// 进程信息
type ProcessInformation struct {
	PID                int               `json:"pid"`                // 进程ID
	TGID               int               `json:"tgid"`               // 线程组ID
	PPID               int               `json:"ppid"`               // 父进程ID
	Command            string            `json:"command"`            // 命令名
	CommandLine        []string          `json:"command_line"`       // 完整命令行
	WorkingDirectory   string            `json:"working_directory"`  // 工作目录
	Environment        map[string]string `json:"environment"`        // 环境变量
}

// 执行上下文
type ExecutionContext struct {
	CurrentFunction    string            `json:"current_function"`   // 当前函数
	CurrentFile        string            `json:"current_file"`       // 当前文件
	CurrentLine        int               `json:"current_line"`       // 当前行号
	ThreadID           int               `json:"thread_id"`          // 线程ID
	CPUContext         CPUContext        `json:"cpu_context"`        // CPU上下文
}

// CPU上下文
type CPUContext struct {
	InstructionPointer uint64            `json:"instruction_pointer"` // 指令指针
	StackPointer       uint64            `json:"stack_pointer"`       // 栈指针
	FramePointer       uint64            `json:"frame_pointer"`       // 帧指针
	Flags              uint64            `json:"flags"`               // 标志寄存器
}

// 变量快照
type VariableSnapshot struct {
	Name               string            `json:"name"`               // 变量名
	Type               string            `json:"type"`               // 变量类型
	Value              interface{}       `json:"value"`              // 变量值
	RawValue           string            `json:"raw_value"`          // 原始值（字符串形式）
	Size               int               `json:"size"`               // 大小（字节）
	Address            uint64            `json:"address"`            // 内存地址
	IsPointer          bool              `json:"is_pointer"`         // 是否为指针
	PointerTarget      *VariableSnapshot `json:"pointer_target,omitempty"` // 指针目标
	ArrayElements      []VariableSnapshot `json:"array_elements,omitempty"` // 数组元素
	StructMembers      []VariableSnapshot `json:"struct_members,omitempty"` // 结构体成员
}

// 调用栈帧
type StackFrame struct {
	Level              int               `json:"level"`              // 栈帧级别
	Function           string            `json:"function"`           // 函数名
	File               string            `json:"file"`               // 文件路径
	Line               int               `json:"line"`               // 行号
	Address            uint64            `json:"address"`            // 地址
	Arguments          []VariableSnapshot `json:"arguments"`         // 函数参数
	LocalVariables     []VariableSnapshot `json:"local_variables"`   // 局部变量
}

// 内存快照
type MemorySnapshot struct {
	StartAddress       uint64            `json:"start_address"`      // 起始地址
	Size               int               `json:"size"`               // 大小
	Data               []byte            `json:"data"`               // 内存数据
	Type               string            `json:"type"`               // 内存类型 "stack", "heap", "code", "data"
	Description        string            `json:"description"`        // 描述
}

// 性能信息
type PerformanceInfo struct {
	CPUUsage           float64           `json:"cpu_usage"`          // CPU使用率
	MemoryUsage        int64             `json:"memory_usage"`       // 内存使用量
	ExecutionTime      int64             `json:"execution_time"`     // 执行时间（纳秒）
	SystemCalls        []SystemCall      `json:"system_calls"`       // 系统调用
}

// 系统调用
type SystemCall struct {
	Name               string            `json:"name"`               // 系统调用名
	Parameters         []interface{}     `json:"parameters"`         // 参数
	ReturnValue        interface{}       `json:"return_value"`       // 返回值
	Duration           int64             `json:"duration"`           // 执行时长（纳秒）
}

// 变量记录（变量监控历史）
type VariableRecord struct {
	ID                 string            `json:"id"`                 // 记录ID
	BreakpointID       string            `json:"breakpoint_id"`      // 关联断点ID
	VariableName       string            `json:"variable_name"`      // 变量名
	Timestamp          time.Time         `json:"timestamp"`          // 记录时间
	OldValue           interface{}       `json:"old_value"`          // 旧值
	NewValue           interface{}       `json:"new_value"`          // 新值
	ValueChanged       bool              `json:"value_changed"`      // 值是否改变
	ChangeType         string            `json:"change_type"`        // 变化类型 "created", "modified", "deleted"
	Context            VariableContext   `json:"context"`            // 变量上下文
}

// 变量上下文
type VariableContext struct {
	Function           string            `json:"function"`           // 所在函数
	File               string            `json:"file"`               // 所在文件
	Line               int               `json:"line"`               // 所在行号
	Scope              string            `json:"scope"`              // 作用域
}

// 调试操作（支持重放）
type DebugOperation struct {
	ID                 string            `json:"id"`                 // 操作ID
	Type               string            `json:"type"`               // 操作类型 "set_breakpoint", "remove_breakpoint", "step", "continue", "inspect_variable"
	Timestamp          time.Time         `json:"timestamp"`          // 操作时间
	User               string            `json:"user"`               // 操作用户
	Parameters         map[string]interface{} `json:"parameters"`    // 操作参数
	Result             OperationResult   `json:"result"`             // 操作结果
	Duration           int64             `json:"duration"`           // 执行时长（毫秒）
}

// 操作结果
type OperationResult struct {
	Success            bool              `json:"success"`            // 是否成功
	Message            string            `json:"message"`            // 结果消息
	Data               map[string]interface{} `json:"data"`          // 结果数据
	Error              string            `json:"error,omitempty"`    // 错误信息
}

// 会话快照
type SessionSnapshot struct {
	ID                 string            `json:"id"`                 // 快照ID
	Name               string            `json:"name"`               // 快照名称
	Timestamp          time.Time         `json:"timestamp"`          // 快照时间
	Description        string            `json:"description"`        // 快照描述
	
	// 状态信息
	BreakpointStates   []ExtendedBreakpoint `json:"breakpoint_states"` // 断点状态
	VariableStates     []VariableSnapshot   `json:"variable_states"`   // 变量状态
	ExecutionState     ExecutionContext     `json:"execution_state"`   // 执行状态
	
	// 快照数据
	MemoryDump         []MemorySnapshot  `json:"memory_dump"`        // 内存转储
	RegisterDump       map[string]interface{} `json:"register_dump"` // 寄存器转储
	CallStackDump      []StackFrame      `json:"call_stack_dump"`    // 调用栈转储
}

// 会话统计
type SessionStatistics struct {
	TotalBreakpoints   int               `json:"total_breakpoints"`   // 总断点数
	TriggeredBreakpoints int             `json:"triggered_breakpoints"` // 触发的断点数
	TotalDebugEvents   int               `json:"total_debug_events"`  // 总调试事件数
	TotalOperations    int               `json:"total_operations"`    // 总操作数
	AverageResponseTime float64          `json:"average_response_time"` // 平均响应时间
	PeakMemoryUsage    int64             `json:"peak_memory_usage"`   // 峰值内存使用
	TotalExecutionTime int64             `json:"total_execution_time"` // 总执行时间
}

// 调试配置
type DebugConfiguration struct {
	MaxBreakpoints     int               `json:"max_breakpoints"`     // 最大断点数
	AutoSaveInterval   int               `json:"auto_save_interval"`  // 自动保存间隔（秒）
	LogLevel           string            `json:"log_level"`           // 日志级别
	EnableProfiling    bool              `json:"enable_profiling"`    // 是否启用性能分析
	MemoryDumpSize     int               `json:"memory_dump_size"`    // 内存转储大小
	MaxEventHistory    int               `json:"max_event_history"`   // 最大事件历史数
	ReplayBufferSize   int               `json:"replay_buffer_size"`  // 重放缓冲区大小
}

// 会话元数据
type SessionMetadata struct {
	Version            string            `json:"version"`             // JSON格式版本
	CreatedBy          string            `json:"created_by"`          // 创建者
	Tags               []string          `json:"tags"`                // 标签
	Notes              string            `json:"notes"`               // 备注
	ExportTime         time.Time         `json:"export_time"`         // 导出时间
	Checksum           string            `json:"checksum"`            // 数据校验和
}

// ================== JSON导出/导入功能 ==================

// 导出完整调试会话到JSON
func (session *DebugSession) ExportToJSON() ([]byte, error) {
	session.Metadata.ExportTime = time.Now()
	session.Metadata.Version = "1.0"
	
	// 计算校验和
	_, err := json.Marshal(session)
	if err != nil {
		return nil, err
	}
	
	// 美化输出
	return json.MarshalIndent(session, "", "  ")
}

// 从JSON导入调试会话
func ImportSessionFromJSON(data []byte) (*DebugSession, error) {
	var session DebugSession
	err := json.Unmarshal(data, &session)
	if err != nil {
		return nil, err
	}
	
	// 验证和修复数据
	session.validate()
	
	return &session, nil
}

// 验证会话数据完整性
func (session *DebugSession) validate() {
	// 确保必要字段不为空
	if session.SessionID == "" {
		session.SessionID = generateSessionID()
	}
	if session.Status == "" {
		session.Status = "ended"
	}
	
	// 验证断点ID唯一性
	session.ensureUniqueBreakpointIDs()
	
	// 验证时间戳顺序
	session.validateTimestamps()
}

// 生成会话ID
func generateSessionID() string {
	return time.Now().Format("20060102_150405_") + randomString(8)
}

// 生成随机字符串
func randomString(length int) string {
	// 简单实现，实际应用中应使用crypto/rand
	return "abcd1234"
}

// 确保断点ID唯一性
func (session *DebugSession) ensureUniqueBreakpointIDs() {
	usedIDs := make(map[string]bool)
	for i := range session.Breakpoints {
		if session.Breakpoints[i].ID == "" || usedIDs[session.Breakpoints[i].ID] {
			session.Breakpoints[i].ID = generateBreakpointID(i)
		}
		usedIDs[session.Breakpoints[i].ID] = true
	}
}

// 生成断点ID
func generateBreakpointID(index int) string {
	return fmt.Sprintf("bp_%d_%s", index, randomString(4))
}

// 验证时间戳顺序
func (session *DebugSession) validateTimestamps() {
	// 确保事件按时间顺序排列
	// 实现细节...
} 