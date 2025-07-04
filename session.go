package main

import (
	"fmt"
	"time"
	"encoding/json"
	"io/ioutil"
	"path/filepath"
	"strings"
	"sort"
)

// ========== 调试会话管理器 ==========

type SessionManager struct {
	ctx *DebuggerContext
}

// NewSessionManager 创建会话管理器
func NewSessionManager(ctx *DebuggerContext) *SessionManager {
	return &SessionManager{ctx: ctx}
}

// ========== 会话初始化和管理 ==========

// InitDebugSession 初始化调试会话
func (sm *SessionManager) InitDebugSession() {
	var projectPath string
	if sm.ctx.Project != nil {
		projectPath = sm.ctx.Project.RootPath
	} else {
		projectPath = "."
	}

	sm.ctx.CurrentSession = &DebugSession{
		SessionInfo: SessionInfo{
			Timestamp:   time.Now(),
			ProjectPath: projectPath,
			TotalFrames: 0,
			Duration:    "",
			Description: "Debug Session",
		},
		Frames:             make([]DebugFrame, 0),
		CurrentFrameIndex: -1,
	}
	
	// 初始化调试模式为实时模式
	sm.ctx.DebugMode = "live"
	sm.ctx.IsRecording = false
	sm.ctx.IsPlayback = false
}

// ========== 录制功能 ==========

// StartRecording 开始录制调试会话
func (sm *SessionManager) StartRecording() error {
	if sm.ctx.Project == nil {
		return fmt.Errorf("请先打开项目")
	}
	
	if sm.ctx.IsRecording {
		return fmt.Errorf("已经在录制中")
	}
	
	// 初始化新的录制会话
	sm.InitDebugSession()
	
	sm.ctx.IsRecording = true
	sm.ctx.DebugMode = "recording"
	sm.ctx.RecordingStartTime = time.Now()
	
	// 更新会话信息
	sm.ctx.CurrentSession.SessionInfo.Description = "Recording Debug Session"
	
	// 启动BPF程序
	bpfManager := NewBPFManager(sm.ctx)
	if err := bpfManager.StartBPFProgram(); err != nil {
		// 如果BPF启动失败，回退录制状态
		sm.ctx.IsRecording = false
		sm.ctx.DebugMode = "live"
		return fmt.Errorf("启动BPF程序失败: %v", err)
	}
	
	return nil
}

// StopRecording 停止录制并保存会话
func (sm *SessionManager) StopRecording() error {
	if !sm.ctx.IsRecording {
		return fmt.Errorf("当前没有在录制")
	}
	
	sm.ctx.IsRecording = false
	sm.ctx.DebugMode = "live"
	
	// 停止BPF程序
	bpfManager := NewBPFManager(sm.ctx)
	bpfManager.StopBPFProgram()
	
	// 计算录制时长
	duration := time.Since(sm.ctx.RecordingStartTime)
	sm.ctx.CurrentSession.SessionInfo.Duration = duration.String()
	sm.ctx.CurrentSession.SessionInfo.TotalFrames = len(sm.ctx.CurrentSession.Frames)
	
	// 自动保存录制会话（按时间戳命名）
	filename := fmt.Sprintf("debug_session_%s.frames", 
		sm.ctx.RecordingStartTime.Format("20060102_150405"))
	
	if err := sm.SaveDebugSession(filename); err != nil {
		return fmt.Errorf("保存录制会话失败: %v", err)
	}
	
	return nil
}

// ========== 会话文件管理 ==========

// SaveDebugSession 保存调试会话到文件
func (sm *SessionManager) SaveDebugSession(filename string) error {
	if sm.ctx.CurrentSession == nil {
		return fmt.Errorf("没有活跃的调试会话")
	}
	
	// 构建完整路径
	var fullPath string
	if sm.ctx.Project != nil {
		fullPath = filepath.Join(sm.ctx.Project.RootPath, filename)
	} else {
		fullPath = filename
	}
	
	// 序列化为JSON
	data, err := json.MarshalIndent(sm.ctx.CurrentSession, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化调试会话失败: %v", err)
	}
	
	// 写入文件
	err = ioutil.WriteFile(fullPath, data, 0644)
	if err != nil {
		return fmt.Errorf("保存调试会话文件失败: %v", err)
	}
	
	return nil
}

// LoadDebugSession 加载调试会话文件
func (sm *SessionManager) LoadDebugSession(filename string) error {
	// 构建完整路径
	var fullPath string
	if sm.ctx.Project != nil {
		fullPath = filepath.Join(sm.ctx.Project.RootPath, filename)
	} else {
		fullPath = filename
	}
	
	// 读取文件
	data, err := ioutil.ReadFile(fullPath)
	if err != nil {
		return fmt.Errorf("读取调试会话文件失败: %v", err)
	}
	
	// 反序列化JSON
	var session DebugSession
	err = json.Unmarshal(data, &session)
	if err != nil {
		return fmt.Errorf("解析调试会话文件失败: %v", err)
	}
	
	// 设置回放模式
	sm.ctx.LoadedSession = &session
	sm.ctx.IsPlayback = true
	sm.ctx.DebugMode = "playback"
	
	// 如果有帧数据，跳转到第一帧
	if len(session.Frames) > 0 {
		sm.ctx.LoadedSession.CurrentFrameIndex = 0
		sm.ctx.CurrentFrame = &session.Frames[0]
		sm.ctx.FrameNavigation.SelectedFrame = 0
	}
	
	return nil
}

// ListDebugSessions 列出可用的调试会话文件（按时间倒序）
func (sm *SessionManager) ListDebugSessions() ([]string, error) {
	var searchPath string
	if sm.ctx.Project != nil {
		searchPath = sm.ctx.Project.RootPath
	} else {
		searchPath = "."
	}
	
	files, err := ioutil.ReadDir(searchPath)
	if err != nil {
		return nil, fmt.Errorf("读取目录失败: %v", err)
	}
	
	type sessionFile struct {
		name    string
		modTime time.Time
	}
	
	var sessions []sessionFile
	for _, file := range files {
		if !file.IsDir() && strings.HasSuffix(file.Name(), ".frames") {
			sessions = append(sessions, sessionFile{
				name:    file.Name(),
				modTime: file.ModTime(),
			})
		}
	}
	
	// 按修改时间倒序排序（最新的在前面）
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].modTime.After(sessions[j].modTime)
	})
	
	var result []string
	for _, session := range sessions {
		result = append(result, session.name)
	}
	
	return result, nil
}

// ========== 帧导航功能 ==========

// JumpToFrame 跳转到指定帧
func (sm *SessionManager) JumpToFrame(frameIndex int) error {
	var session *DebugSession
	
	if sm.ctx.IsPlayback && sm.ctx.LoadedSession != nil {
		session = sm.ctx.LoadedSession
	} else if sm.ctx.CurrentSession != nil {
		session = sm.ctx.CurrentSession
	} else {
		return fmt.Errorf("没有可用的调试会话")
	}
	
	if frameIndex < 0 || frameIndex >= len(session.Frames) {
		return fmt.Errorf("帧索引超出范围: %d", frameIndex)
	}
	
	// 设置当前帧
	session.CurrentFrameIndex = frameIndex
	sm.ctx.CurrentFrame = &session.Frames[frameIndex]
	sm.ctx.FrameNavigation.SelectedFrame = frameIndex
	
	return nil
}

// NextFrame 跳转到下一帧
func (sm *SessionManager) NextFrame() error {
	var session *DebugSession
	
	if sm.ctx.IsPlayback && sm.ctx.LoadedSession != nil {
		session = sm.ctx.LoadedSession
	} else if sm.ctx.CurrentSession != nil {
		session = sm.ctx.CurrentSession
	} else {
		return fmt.Errorf("没有可用的调试会话")
	}
	
	currentIndex := session.CurrentFrameIndex
	if currentIndex >= len(session.Frames)-1 {
		return fmt.Errorf("已经是最后一帧")
	}
	
	return sm.JumpToFrame(currentIndex + 1)
}

// PrevFrame 跳转到上一帧
func (sm *SessionManager) PrevFrame() error {
	var session *DebugSession
	
	if sm.ctx.IsPlayback && sm.ctx.LoadedSession != nil {
		session = sm.ctx.LoadedSession
	} else if sm.ctx.CurrentSession != nil {
		session = sm.ctx.CurrentSession
	} else {
		return fmt.Errorf("没有可用的调试会话")
	}
	
	currentIndex := session.CurrentFrameIndex
	if currentIndex <= 0 {
		return fmt.Errorf("已经是第一帧")
	}
	
	return sm.JumpToFrame(currentIndex - 1)
}

// GetCurrentFrameInfo 获取当前帧信息
func (sm *SessionManager) GetCurrentFrameInfo() string {
	var session *DebugSession
	
	if sm.ctx.IsPlayback && sm.ctx.LoadedSession != nil {
		session = sm.ctx.LoadedSession
	} else if sm.ctx.CurrentSession != nil {
		session = sm.ctx.CurrentSession
	} else {
		return "没有可用的调试会话"
	}
	
	if len(session.Frames) == 0 {
		return "会话中没有帧数据"
	}
	
	currentIndex := session.CurrentFrameIndex
	if currentIndex < 0 || currentIndex >= len(session.Frames) {
		return "无效的帧索引"
	}
	
	frame := &session.Frames[currentIndex]
	return fmt.Sprintf("帧 %d/%d - %s - %s()", 
		currentIndex+1, 
		len(session.Frames),
		frame.Timestamp.Format("15:04:05.000"),
		frame.BreakpointInfo.Function)
} 