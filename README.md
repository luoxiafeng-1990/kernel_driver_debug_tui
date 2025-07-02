# debug-gocui

本目录为基于 Go 语言 gocui 框架的内核调试器 TUI 新实现。

## 主要特性
- 多窗口布局（寄存器、变量、调用栈、内存、代码、命令、状态栏）
- 支持窗口切换、滚动、命令输入、eBPF 加载、断点、单步、继续等调试功能
- 适配嵌入式 Linux 终端环境

## 依赖
- Go 1.20+
- gocui (已在 go.mod 中声明)

## 运行
```sh
cd debug-gocui
# 拉取依赖
go mod tidy
# 运行
go run main.go
```

## 后续扩展
- 可根据原 C 版 kernel_debugger_tui.c 逐步补全窗口内容刷新、调试命令、eBPF 交互等功能。
