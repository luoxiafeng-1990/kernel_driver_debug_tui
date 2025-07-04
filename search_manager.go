package main

import (
	"fmt"
	"strings"
	"regexp"
	"unicode"
	"path/filepath"
	
	"github.com/jroimartin/gocui"
)

// ========== 搜索管理器 ==========

type SearchManager struct {
	ctx *DebuggerContext
	gui *gocui.Gui
}

// NewSearchManager 创建搜索管理器
func NewSearchManager(ctx *DebuggerContext, gui *gocui.Gui) *SearchManager {
	return &SearchManager{ctx: ctx, gui: gui}
}

// ========== 搜索功能 ==========

// StartSearch 开始搜索模式
func (sm *SearchManager) StartSearch(g *gocui.Gui, v *gocui.View) error {
	sm.ctx.SearchMode = true
	sm.ctx.SearchInput = ""
	sm.ctx.SearchTerm = ""
	sm.ctx.SearchResults = []SearchResult{}
	sm.ctx.CurrentMatch = -1
	
	// 重新绘制代码视图以显示搜索状态
	g.Update(func(g *gocui.Gui) error {
		viewUpdater := NewViewUpdater(sm.ctx, g)
		viewUpdater.UpdateCodeView(g, sm.ctx)
		return nil
	})
	
	return nil
}

// ExitSearch 退出搜索模式
func (sm *SearchManager) ExitSearch(g *gocui.Gui, v *gocui.View) error {
	sm.ctx.SearchMode = false
	sm.ctx.SearchInput = ""
	sm.ctx.SearchTerm = ""
	sm.ctx.SearchResults = []SearchResult{}
	sm.ctx.CurrentMatch = -1
	
	// 重新绘制代码视图以清除搜索状态
	g.Update(func(g *gocui.Gui) error {
		viewUpdater := NewViewUpdater(sm.ctx, g)
		viewUpdater.UpdateCodeView(g, sm.ctx)
		return nil
	})
	
	return nil
}

// HandleSearchInput 处理搜索输入
func (sm *SearchManager) HandleSearchInput(g *gocui.Gui, v *gocui.View, ch rune) error {
	if !sm.ctx.SearchMode {
		return nil
	}
	
	// 只接受可打印字符
	if unicode.IsPrint(ch) {
		sm.ctx.SearchInput += string(ch)
		
		// 实时搜索
		if len(sm.ctx.SearchInput) > 0 {
			sm.performSearch()
		}
		
		// 更新代码视图以显示搜索结果
		g.Update(func(g *gocui.Gui) error {
			viewUpdater := NewViewUpdater(sm.ctx, g)
			viewUpdater.UpdateCodeView(g, sm.ctx)
			return nil
		})
	}
	
	return nil
}

// HandleSearchBackspace 处理搜索退格
func (sm *SearchManager) HandleSearchBackspace(g *gocui.Gui, v *gocui.View) error {
	if !sm.ctx.SearchMode {
		return nil
	}
	
	if len(sm.ctx.SearchInput) > 0 {
		sm.ctx.SearchInput = sm.ctx.SearchInput[:len(sm.ctx.SearchInput)-1]
		
		// 重新搜索
		if len(sm.ctx.SearchInput) > 0 {
			sm.performSearch()
		} else {
			// 清空搜索结果
			sm.ctx.SearchTerm = ""
			sm.ctx.SearchResults = []SearchResult{}
			sm.ctx.CurrentMatch = -1
		}
		
		// 更新代码视图
		g.Update(func(g *gocui.Gui) error {
			viewUpdater := NewViewUpdater(sm.ctx, g)
			viewUpdater.UpdateCodeView(g, sm.ctx)
			return nil
		})
	}
	
	return nil
}

// HandleSearchEnter 处理搜索回车
func (sm *SearchManager) HandleSearchEnter(g *gocui.Gui, v *gocui.View) error {
	if !sm.ctx.SearchMode {
		return nil
	}
	
	if len(sm.ctx.SearchInput) > 0 {
		sm.ctx.SearchTerm = sm.ctx.SearchInput
		sm.performSearch()
		
		// 跳转到第一个匹配
		if len(sm.ctx.SearchResults) > 0 {
			sm.ctx.CurrentMatch = 0
			sm.jumpToMatch(0)
		}
		
		// 更新代码视图
		g.Update(func(g *gocui.Gui) error {
			viewUpdater := NewViewUpdater(sm.ctx, g)
			viewUpdater.UpdateCodeView(g, sm.ctx)
			return nil
		})
	}
	
	return nil
}

// NextMatch 跳转到下一个匹配
func (sm *SearchManager) NextMatch(g *gocui.Gui, v *gocui.View) error {
	if !sm.ctx.SearchMode || len(sm.ctx.SearchResults) == 0 {
		return nil
	}
	
	sm.ctx.CurrentMatch = (sm.ctx.CurrentMatch + 1) % len(sm.ctx.SearchResults)
	sm.jumpToMatch(sm.ctx.CurrentMatch)
	
	// 更新代码视图
	g.Update(func(g *gocui.Gui) error {
		viewUpdater := NewViewUpdater(sm.ctx, g)
		viewUpdater.UpdateCodeView(g, sm.ctx)
		return nil
	})
	
	return nil
}

// PrevMatch 跳转到上一个匹配
func (sm *SearchManager) PrevMatch(g *gocui.Gui, v *gocui.View) error {
	if !sm.ctx.SearchMode || len(sm.ctx.SearchResults) == 0 {
		return nil
	}
	
	sm.ctx.CurrentMatch = (sm.ctx.CurrentMatch - 1 + len(sm.ctx.SearchResults)) % len(sm.ctx.SearchResults)
	sm.jumpToMatch(sm.ctx.CurrentMatch)
	
	// 更新代码视图
	g.Update(func(g *gocui.Gui) error {
		viewUpdater := NewViewUpdater(sm.ctx, g)
		viewUpdater.UpdateCodeView(g, sm.ctx)
		return nil
	})
	
	return nil
}

// ========== 搜索实现 ==========

// performSearch 执行搜索
func (sm *SearchManager) performSearch() {
	if sm.ctx.Project == nil || sm.ctx.Project.CurrentFile == "" || sm.ctx.SearchInput == "" {
		return
	}
	
	// 获取当前文件内容
	lines, exists := sm.ctx.Project.OpenFiles[sm.ctx.Project.CurrentFile]
	if !exists {
		fileManager := NewFileManager(sm.ctx)
		content, err := fileManager.GetCurrentFileContent()
		if err != nil {
			return
		}
		lines = content
		sm.ctx.Project.OpenFiles[sm.ctx.Project.CurrentFile] = lines
	}
	
	// 清空之前的搜索结果
	sm.ctx.SearchResults = []SearchResult{}
	sm.ctx.SearchTerm = sm.ctx.SearchInput
	
	// 检查是否是正则表达式搜索
	isRegex := sm.isRegexPattern(sm.ctx.SearchInput)
	
	// 在每一行中搜索
	for lineIndex, line := range lines {
		lineNum := lineIndex + 1
		
		if isRegex {
			sm.searchRegexInLine(line, lineNum)
		} else {
			sm.searchTextInLine(line, lineNum)
		}
	}
	
	// 如果有搜索结果，设置当前匹配为第一个
	if len(sm.ctx.SearchResults) > 0 {
		sm.ctx.CurrentMatch = 0
	} else {
		sm.ctx.CurrentMatch = -1
	}
}

// isRegexPattern 检查是否是正则表达式模式
func (sm *SearchManager) isRegexPattern(pattern string) bool {
	// 简单的正则表达式检测
	regexChars := []string{".*", ".+", "^", "$", "[", "]", "(", ")", "{", "}", "\\", "|"}
	for _, char := range regexChars {
		if strings.Contains(pattern, char) {
			return true
		}
	}
	return false
}

// searchTextInLine 在行中搜索文本
func (sm *SearchManager) searchTextInLine(line string, lineNum int) {
	searchTerm := sm.ctx.SearchInput
	
	// 不区分大小写的搜索
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
		
		sm.ctx.SearchResults = append(sm.ctx.SearchResults, result)
		startIndex = actualIndex + 1
	}
}

// searchRegexInLine 在行中搜索正则表达式
func (sm *SearchManager) searchRegexInLine(line string, lineNum int) {
	pattern := sm.ctx.SearchInput
	
	// 编译正则表达式
	regex, err := regexp.Compile(pattern)
	if err != nil {
		// 如果正则表达式无效，退回到普通文本搜索
		sm.searchTextInLine(line, lineNum)
		return
	}
	
	// 查找所有匹配
	matches := regex.FindAllStringSubmatchIndex(line, -1)
	for _, match := range matches {
		if len(match) >= 2 {
			startIndex := match[0]
			endIndex := match[1]
			
			result := SearchResult{
				LineNumber:  lineNum,
				StartColumn: startIndex,
				EndColumn:   endIndex,
				Text:        line[startIndex:endIndex],
			}
			
			sm.ctx.SearchResults = append(sm.ctx.SearchResults, result)
		}
	}
}

// jumpToMatch 跳转到指定匹配
func (sm *SearchManager) jumpToMatch(matchIndex int) {
	if matchIndex < 0 || matchIndex >= len(sm.ctx.SearchResults) {
		return
	}
	
	match := sm.ctx.SearchResults[matchIndex]
	
	// 调整代码视图滚动位置以显示匹配行
	v, err := sm.gui.View("code")
	if err != nil {
		return
	}
	
	// 计算窗口大小
	_, viewHeight := v.Size()
	headerLines := 2 // 标题行数
	availableLines := viewHeight - headerLines
	if availableLines < 1 {
		availableLines = 1
	}
	
	// 计算目标行的滚动位置（让匹配行显示在视图中央）
	targetLine := match.LineNumber - 1 // 转换为0基索引
	centerOffset := availableLines / 2
	
	newScroll := targetLine - centerOffset
	if newScroll < 0 {
		newScroll = 0
	}
	
	// 更新全局滚动变量
	codeScroll = newScroll
}

// ========== 搜索高亮功能 ==========

// HighlightSearchMatches 高亮搜索匹配
func (sm *SearchManager) HighlightSearchMatches(line string, lineNum int) string {
	if !sm.ctx.SearchMode || sm.ctx.SearchTerm == "" {
		return line
	}
	
	highlightedLine := line
	
	// 查找该行的所有搜索结果
	lineResults := []SearchResult{}
	for _, result := range sm.ctx.SearchResults {
		if result.LineNumber == lineNum {
			lineResults = append(lineResults, result)
		}
	}
	
	// 从后往前高亮，避免位置偏移问题
	for i := len(lineResults) - 1; i >= 0; i-- {
		result := lineResults[i]
		
		// 检查是否是当前匹配
		isCurrentMatch := false
		if sm.ctx.CurrentMatch >= 0 && sm.ctx.CurrentMatch < len(sm.ctx.SearchResults) {
			currentResult := sm.ctx.SearchResults[sm.ctx.CurrentMatch]
			if currentResult.LineNumber == result.LineNumber && 
			   currentResult.StartColumn == result.StartColumn {
				isCurrentMatch = true
			}
		}
		
		before := highlightedLine[:result.StartColumn]
		match := highlightedLine[result.StartColumn:result.EndColumn]
		after := highlightedLine[result.EndColumn:]
		
		// 根据是否是当前匹配使用不同颜色
		if isCurrentMatch {
			// 当前匹配：红色背景
			highlightedLine = before + "\x1b[41;37m" + match + "\x1b[0m" + after
		} else {
			// 其他匹配：黄色背景
			highlightedLine = before + "\x1b[43;30m" + match + "\x1b[0m" + after
		}
	}
	
	return highlightedLine
}

// ========== 搜索状态管理 ==========

// GetSearchStatus 获取搜索状态字符串
func (sm *SearchManager) GetSearchStatus() string {
	if !sm.ctx.SearchMode {
		return ""
	}
	
	if len(sm.ctx.SearchResults) > 0 {
		return fmt.Sprintf("Search: \"%s\" (%d/%d)", 
			sm.ctx.SearchTerm, sm.ctx.CurrentMatch+1, len(sm.ctx.SearchResults))
	} else if sm.ctx.SearchTerm != "" {
		return fmt.Sprintf("Search: \"%s\" (no results)", sm.ctx.SearchTerm)
	} else {
		return fmt.Sprintf("Search: \"%s\"", sm.ctx.SearchInput)
	}
}

// ClearSearch 清空搜索
func (sm *SearchManager) ClearSearch() {
	sm.ctx.SearchMode = false
	sm.ctx.SearchInput = ""
	sm.ctx.SearchTerm = ""
	sm.ctx.SearchResults = []SearchResult{}
	sm.ctx.CurrentMatch = -1
}

// ========== 全局搜索功能 ==========

// SearchInAllFiles 在所有文件中搜索
func (sm *SearchManager) SearchInAllFiles(searchTerm string) map[string][]SearchResult {
	results := make(map[string][]SearchResult)
	
	if sm.ctx.Project == nil || sm.ctx.Project.FileTree == nil {
		return results
	}
	
	// 遍历所有文件
	sm.searchInFileTree(sm.ctx.Project.FileTree, searchTerm, results)
	
	return results
}

// searchInFileTree 递归搜索文件树
func (sm *SearchManager) searchInFileTree(node *FileNode, searchTerm string, results map[string][]SearchResult) {
	if node == nil {
		return
	}
	
	if node.IsDir {
		// 递归搜索子目录
		for _, child := range node.Children {
			sm.searchInFileTree(child, searchTerm, results)
		}
	} else {
		// 搜索文件
		if sm.isSearchableFile(node.Name) {
			fileResults := sm.searchInFile(node.Path, searchTerm)
			if len(fileResults) > 0 {
				results[node.Path] = fileResults
			}
		}
	}
}

// isSearchableFile 检查文件是否可搜索
func (sm *SearchManager) isSearchableFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	searchableExts := []string{".c", ".cpp", ".h", ".hpp", ".go", ".py", ".js", ".ts", ".java", ".txt", ".md", ".json", ".xml", ".html", ".css"}
	
	for _, searchableExt := range searchableExts {
		if ext == searchableExt {
			return true
		}
	}
	
	return false
}

// searchInFile 在单个文件中搜索
func (sm *SearchManager) searchInFile(filePath, searchTerm string) []SearchResult {
	results := []SearchResult{}
	
	// 读取文件内容
	fileManager := NewFileManager(sm.ctx)
	lines, err := fileManager.GetFileContent(filePath)
	if err != nil {
		return results
	}
	
	// 搜索每一行
	for lineIndex, line := range lines {
		lineNum := lineIndex + 1
		
		// 不区分大小写的搜索
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

// ========== 搜索历史管理 ==========

// AddSearchToHistory 添加搜索到历史
func (sm *SearchManager) AddSearchToHistory(searchTerm string) {
	if searchTerm == "" {
		return
	}
	
	// 检查是否已存在
	for i, term := range sm.ctx.SearchHistory {
		if term == searchTerm {
			// 移动到最前面
			sm.ctx.SearchHistory = append([]string{searchTerm}, append(sm.ctx.SearchHistory[:i], sm.ctx.SearchHistory[i+1:]...)...)
			return
		}
	}
	
	// 添加到最前面
	sm.ctx.SearchHistory = append([]string{searchTerm}, sm.ctx.SearchHistory...)
	
	// 限制历史长度
	if len(sm.ctx.SearchHistory) > 50 {
		sm.ctx.SearchHistory = sm.ctx.SearchHistory[:50]
	}
}

// GetSearchHistory 获取搜索历史
func (sm *SearchManager) GetSearchHistory() []string {
	return sm.ctx.SearchHistory
}

// ========== 搜索快捷功能 ==========

// FindNext 查找下一个（F3）
func (sm *SearchManager) FindNext(g *gocui.Gui, v *gocui.View) error {
	if len(sm.ctx.SearchResults) > 0 {
		return sm.NextMatch(g, v)
	}
	return nil
}

// FindPrev 查找上一个（Shift+F3）
func (sm *SearchManager) FindPrev(g *gocui.Gui, v *gocui.View) error {
	if len(sm.ctx.SearchResults) > 0 {
		return sm.PrevMatch(g, v)
	}
	return nil
}

// QuickSearch 快速搜索（Ctrl+F）
func (sm *SearchManager) QuickSearch(g *gocui.Gui, v *gocui.View) error {
	return sm.StartSearch(g, v)
} 