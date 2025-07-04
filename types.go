package main

import (
	"time"
	"context"
	"github.com/jroimartin/gocui"
)

// ========== 基础类型 ==========

// 调试器状态
const (
	DEBUG_STOPPED = iota
	DEBUG_RUNNING
	DEBUG_STEPPING
	DEBUG_BREAKPOINT
)

type DebuggerState int

// ========== 项目和文件管理 ==========

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

// ========== BPF相关数据结构 ==========

// BPF调试事件结构（与BPF程序中的结构体保持完全一致）
type BPFDebugEvent struct {
	PID         uint32
	TGID        uint32
	Timestamp   uint64
	BreakpointID uint32
	Comm        [16]byte
	Function    [64]byte
	
	// RISC-V寄存器状态
	PC, RA, SP, GP, TP          uint64
	T0, T1, T2                  uint64
	S0, S1                      uint64
	A0, A1, A2, A3, A4, A5, A6, A7 uint64
	
	// 栈数据和局部变量
	StackData [8]uint64
	LocalVars [16]uint64
}

// BPF映射文件描述符
type BPFMaps struct {
	EventsMap  int // 事件环形缓冲区
	ControlMap int // 控制映射
}

// BPF程序上下文
type BPFContext struct {
	ProgramFD int
	Maps      BPFMaps
	Cancel    context.CancelFunc
	Running   bool
}

// ========== 调试帧系统 ==========

// 调试帧 - 包含一次断点触发的完整状态
type DebugFrame struct {
	FrameID      int                `json:"frame_id"`
	Timestamp    time.Time          `json:"timestamp"`
	BreakpointInfo Breakpoint       `json:"breakpoint"`
	
	// 寄存器状态
	Registers    map[string]uint64  `json:"registers"`
	
	// 变量信息
	LocalVariables  map[string]interface{} `json:"local_variables"`
	GlobalVariables map[string]interface{} `json:"global_variables"`
	
	// 堆栈信息
	StackFrames     []StackFrame      `json:"stack_frames"`
	StackData       []uint64          `json:"stack_data"`
	
	// 函数调用链
	CallChain       []CallFrame       `json:"call_chain"`
	
	// 原始BPF事件数据
	RawBPFEvent     *BPFDebugEvent    `json:"raw_bpf_event,omitempty"`
}

// 栈帧信息
type StackFrame struct {
	FunctionName string `json:"function_name"`
	FileName     string `json:"file_name"`
	LineNumber   int    `json:"line_number"`
	Address      uint64 `json:"address"`
}

// 调用帧信息
type CallFrame struct {
	FunctionName   string `json:"function_name"`
	ReturnAddress  uint64 `json:"return_address"`
	Arguments      []uint64 `json:"arguments"`
}

// 调试会话
type DebugSession struct {
	SessionInfo   SessionInfo  `json:"session_info"`
	Frames        []DebugFrame `json:"frames"`
	CurrentFrameIndex int      `json:"-"` // 当前浏览的帧索引（不保存到文件）
}

// 会话信息
type SessionInfo struct {
	Timestamp     time.Time `json:"timestamp"`
	ProjectPath   string    `json:"project_path"`
	TotalFrames   int       `json:"total_frames"`
	Duration      string    `json:"duration"`
	Description   string    `json:"description"`
}

// ========== UI相关 ==========

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

// 弹出窗口结构
type PopupWindow struct {
	ID         string   // 窗口唯一标识
	Title      string   // 窗口标题
	X, Y       int      // 窗口左上角位置
	Width      int      // 窗口宽度  
	Height     int      // 窗口高度
	Content    []string // 窗口内容（按行存储）
	Visible    bool     // 是否可见
	Dragging   bool     // 是否正在拖拽
	DragStartX int      // 拖拽起始X坐标
	DragStartY int      // 拖拽起始Y坐标
	ScrollY    int      // 垂直滚动偏移
}

// 搜索结果结构
type SearchResult struct {
	LineNumber  int // 行号（从1开始）
	StartColumn int // 匹配开始列（从0开始）
	EndColumn   int // 匹配结束列（从0开始）
	Text        string // 匹配的文本
}

// ========== 主调试器上下文 ==========

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
	
	// 命令窗口状态管理
	CommandHistory []string  // 保存所有命令历史（包括命令和输出）
	CurrentInput   string    // 当前正在输入的命令
	CommandDirty   bool      // 标记命令窗口是否需要重绘
	
	// 双击检测状态
	LastClickTime  time.Time // 上次点击时间
	LastClickLine  int       // 上次点击的行号
	
	// 全屏状态管理
	IsFullscreen   bool          // 是否处于全屏状态
	FullscreenView string        // 当前全屏的窗口名称
	SavedLayout    *DynamicLayout // 保存的原始布局
	
	// 弹出窗口系统
	PopupWindows   []*PopupWindow // 所有弹出窗口列表
	DraggingPopup  *PopupWindow  // 当前正在拖拽的弹出窗口
	
	// 代码搜索系统
	SearchMode     bool          // 是否处于搜索模式
	SearchTerm     string        // 当前搜索词
	SearchResults  []SearchResult // 搜索结果列表
	CurrentMatch   int           // 当前匹配项索引
	SearchInput    string        // 搜索输入缓冲区
	SearchDirty    bool          // 搜索结果是否需要更新
	
	// ========== 调试帧录制回放系统 ==========
	// 调试模式
	DebugMode      string        // "live", "recording", "playback"
	
	// 当前调试会话
	CurrentSession *DebugSession  // 当前活跃的调试会话
	
	// 录制相关
	IsRecording    bool          // 是否正在录制
	RecordingStartTime time.Time // 录制开始时间
	
	// 回放相关
	IsPlayback     bool          // 是否处于回放模式
	LoadedSession  *DebugSession // 加载的调试会话（用于回放）
	
	// 实时调试数据（录制模式下的当前帧）
	CurrentFrame   *DebugFrame   // 当前帧数据
	
	// BPF数据接收
	BPFDataChannel chan *BPFDebugEvent // BPF事件数据通道
	BPFCtx         *BPFContext         // BPF程序上下文
	GUI            *gocui.Gui          // GUI引用（用于更新界面）
	
	// 帧导航状态
	FrameNavigation struct {
		ShowTimeline  bool          // 是否显示时间线
		TimelineMode  string        // "compact", "full"
		SelectedFrame int           // 用户选中的帧
	}
} 