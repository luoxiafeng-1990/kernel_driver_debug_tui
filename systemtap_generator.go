package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ================== SystemTap脚本生成器 ==================

// SystemTap脚本生成器
func generateSystemTapScript(ctx *DebuggerContext, outputJSONFile string) error {
	if ctx.Project == nil || len(ctx.Project.Breakpoints) == 0 {
		return fmt.Errorf("没有设置断点")
	}

	// 生成脚本文件路径
	scriptPath := filepath.Join(ctx.Project.RootPath, "debug_monitor.stp")

	// 创建SystemTap脚本文件
	file, err := os.Create(scriptPath)
	if err != nil {
		return fmt.Errorf("创建SystemTap脚本文件失败: %v", err)
	}
	defer file.Close()

	// 写入脚本头部
	writeSystemTapHeader(file, ctx)

	// 写入JSON输出相关的全局变量和函数
	writeJSONSupport(file, outputJSONFile)

	// 检查内核调试信息并写入兼容性处理
	writeDebugInfoHandling(file, ctx)
	
	// 写入断点探针
	writeBreakpointProbes(file, ctx)

	// 写入脚本尾部
	writeSystemTapFooter(file)

	// 生成执行脚本
	err = generateExecutionScript(ctx, scriptPath)
	if err != nil {
		return fmt.Errorf("生成执行脚本失败: %v", err)
	}

	return nil
}

// 写入SystemTap脚本头部
func writeSystemTapHeader(file *os.File, ctx *DebuggerContext) {
	fmt.Fprintln(file, "#!/usr/bin/env stap")
	fmt.Fprintln(file, "# SystemTap调试脚本 - 自动生成")
	fmt.Fprintf(file, "# 生成时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(file, "# 项目路径: %s\n", ctx.Project.RootPath)
	fmt.Fprintf(file, "# 断点数量: %d\n", len(ctx.Project.Breakpoints))
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 全局变量")
	fmt.Fprintln(file, "global session_id")
	fmt.Fprintln(file, "global session_start_time")
	fmt.Fprintln(file, "global breakpoint_count")
	fmt.Fprintln(file, "global debug_events_count")
	fmt.Fprintln(file, "global json_output_file")
	fmt.Fprintln(file, "global first_json_output = 1")
	fmt.Fprintln(file, "")
}

// 写入JSON支持函数
func writeJSONSupport(file *os.File, outputFile string) {
	fmt.Fprintln(file, "# JSON输出支持函数")
	fmt.Fprintln(file, "")

	// 初始化函数
	fmt.Fprintln(file, "function init_json_output() {")
	fmt.Fprintln(file, "    # 动态构建JSON输出路径（使用$HOME环境变量）")
	fmt.Fprintln(file, "    json_output_file = \"$HOME/.systemtap/debug_session.json\"")
	fmt.Fprintln(file, "    ")
	fmt.Fprintln(file, "    # 确保.systemtap目录存在")
	fmt.Fprintln(file, "    system(\"mkdir -p $HOME/.systemtap\")")
	fmt.Fprintln(file, "    session_id = sprintf(\"session_%d_%s\", gettimeofday_s(), execname())")
	fmt.Fprintln(file, "    session_start_time = gettimeofday_ns()")
	fmt.Fprintln(file, "    breakpoint_count = 0")
	fmt.Fprintln(file, "    debug_events_count = 0")
	fmt.Fprintln(file, "    ")
	fmt.Fprintln(file, "    # 创建JSON文件并写入头部")
	fmt.Fprintln(file, "    system(sprintf(\"echo '{' > %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  \\\"session_id\\\": \\\"%s\\\",' >> %s\", session_id, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  \\\"session_name\\\": \\\"SystemTap调试会话\\\",' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  \\\"start_time\\\": \\\"%s\\\",' >> %s\", ctime(gettimeofday_s()), json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  \\\"status\\\": \\\"active\\\",' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  \\\"debug_events\\\": [' >> %s\", json_output_file))")
	fmt.Fprintln(file, "}")
	fmt.Fprintln(file, "")

	// JSON事件输出函数
	fmt.Fprintln(file, "function output_debug_event(breakpoint_id, function_name, pid, tid, timestamp, variables) {")
	fmt.Fprintln(file, "    debug_events_count++")
	fmt.Fprintln(file, "    ")
	fmt.Fprintln(file, "    # 如果不是第一个事件，添加逗号")
	fmt.Fprintln(file, "    if (first_json_output) {")
	fmt.Fprintln(file, "        first_json_output = 0")
	fmt.Fprintln(file, "    } else {")
	fmt.Fprintln(file, "        system(sprintf(\"echo ',' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    }")
	fmt.Fprintln(file, "    ")
	fmt.Fprintln(file, "    # 输出JSON事件")
	fmt.Fprintln(file, "    system(sprintf(\"echo '    {' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      \\\"id\\\": \\\"debug_%d\\\",' >> %s\", debug_events_count, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      \\\"breakpoint_id\\\": \\\"bp_%03d\\\",' >> %s\", breakpoint_id, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      \\\"timestamp\\\": \\\"%s\\\",' >> %s\", ctime(gettimeofday_s()), json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      \\\"function\\\": \\\"%s\\\",' >> %s\", function_name, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      \\\"process_info\\\": {' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '        \\\"pid\\\": %d,' >> %s\", pid, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '        \\\"tid\\\": %d,' >> %s\", tid, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '        \\\"command\\\": \\\"%s\\\"' >> %s\", execname(), json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      },' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      \\\"execution_context\\\": {' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '        \\\"current_function\\\": \\\"%s\\\"' >> %s\", function_name, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      },' >> %s\", json_output_file))")

	// 变量快照输出
	fmt.Fprintln(file, "    system(sprintf(\"echo '      \\\"variable_snapshots\\\": [' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    if (variables != \"\") {")
	fmt.Fprintln(file, "        system(sprintf(\"echo '%s' >> %s\", variables, json_output_file))")
	fmt.Fprintln(file, "    }")
	fmt.Fprintln(file, "    system(sprintf(\"echo '      ]' >> %s\", json_output_file))")

	fmt.Fprintln(file, "    system(sprintf(\"echo '    }' >> %s\", json_output_file))")
	fmt.Fprintln(file, "}")
	fmt.Fprintln(file, "")

	// 会话结束函数
	fmt.Fprintln(file, "function finalize_json_output() {")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  ],' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  \\\"statistics\\\": {' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '    \\\"total_debug_events\\\": %d,' >> %s\", debug_events_count, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '    \\\"total_execution_time\\\": %d' >> %s\", gettimeofday_ns() - session_start_time, json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  },' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  \\\"metadata\\\": {' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '    \\\"version\\\": \\\"1.0\\\",' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '    \\\"created_by\\\": \\\"SystemTap Script Generator\\\",' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '    \\\"export_time\\\": \\\"%s\\\"' >> %s\", ctime(gettimeofday_s()), json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '  }' >> %s\", json_output_file))")
	fmt.Fprintln(file, "    system(sprintf(\"echo '}' >> %s\", json_output_file))")
	fmt.Fprintln(file, "}")
	fmt.Fprintln(file, "")
}

// 检测项目是否为内核模块
func isKernelModule(ctx *DebuggerContext) (bool, string) {
	// 首先尝试从Makefile中读取模块名
	moduleName := parseModuleNameFromMakefile(ctx.Project.RootPath)
	if moduleName != "" {
		return true, moduleName
	}
	
	// 检查项目目录中是否有.ko文件
	matches, err := filepath.Glob(filepath.Join(ctx.Project.RootPath, "*.ko"))
	if err == nil && len(matches) > 0 {
		// 从.ko文件名中提取模块名
		koFile := filepath.Base(matches[0])
		moduleName := strings.TrimSuffix(koFile, ".ko")
		return true, moduleName
	}
	
	// 检查源文件中是否包含内核模块的头文件
	for _, file := range ctx.Project.OpenFiles {
		for _, line := range file {
			if strings.Contains(line, "#include <linux/module.h>") ||
			   strings.Contains(line, "MODULE_LICENSE") ||
			   strings.Contains(line, "module_init") ||
			   strings.Contains(line, "module_exit") {
				// 尝试从项目目录名推断模块名
				projectName := filepath.Base(ctx.Project.RootPath)
				// 如果目录名包含 "ko"，去掉后缀
				if strings.HasSuffix(projectName, "_ko") {
					projectName = strings.TrimSuffix(projectName, "_ko")
				} else if strings.HasSuffix(projectName, "ko") {
					projectName = strings.TrimSuffix(projectName, "ko")
				}
				return true, projectName
			}
		}
	}
	
	return false, ""
}

// 从Makefile中解析模块名
func parseModuleNameFromMakefile(projectPath string) string {
	makefilePath := filepath.Join(projectPath, "Makefile")
	
	// 尝试读取Makefile
	content, err := ioutil.ReadFile(makefilePath)
	if err != nil {
		return ""
	}
	
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		
		// 查找 MODULE_NAME := xxx 模式
		if strings.HasPrefix(line, "MODULE_NAME") && strings.Contains(line, ":=") {
			parts := strings.Split(line, ":=")
			if len(parts) >= 2 {
				moduleName := strings.TrimSpace(parts[1])
				if moduleName != "" {
					return moduleName
				}
			}
		}
		
		// 查找 obj-m += xxx.o 或 obj-m := xxx.o 模式
		if strings.HasPrefix(line, "obj-m") && (strings.Contains(line, "+=") || strings.Contains(line, ":=")) {
			var parts []string
			if strings.Contains(line, "+=") {
				parts = strings.Split(line, "+=")
			} else {
				parts = strings.Split(line, ":=")
			}
			if len(parts) >= 2 {
				objName := strings.TrimSpace(parts[1])
				if strings.HasSuffix(objName, ".o") {
					moduleName := strings.TrimSuffix(objName, ".o")
					if moduleName != "" {
						return moduleName
					}
				}
			}
		}
	}
	
	return ""
}

// 写入调试信息兼容性处理
func writeDebugInfoHandling(file *os.File, ctx *DebuggerContext) {
	fmt.Fprintln(file, "# 内核调试信息兼容性处理")
	fmt.Fprintln(file, "")
	
	// 检查是否为内核模块
	isKernel, moduleName := isKernelModule(ctx)
	
	if isKernel {
		fmt.Fprintln(file, "# 检查内核模块调试信息")
		fmt.Fprintln(file, "probe begin {")
		fmt.Fprintf(file, "    printf(\"开始监控内核模块: %s\\n\")\n", moduleName)
		fmt.Fprintln(file, "    printf(\"内核版本: \"); system(\"uname -r\")")
		fmt.Fprintln(file, "    printf(\"架构: \"); system(\"uname -m\")")
		fmt.Fprintln(file, "    ")
		fmt.Fprintln(file, "    # 检查模块是否已加载")
		fmt.Fprintf(file, "    if (system(\"lsmod | grep -q %s\") != 0) {\n", moduleName)
		fmt.Fprintf(file, "        printf(\"警告: 模块 %s 未加载，请先加载模块\\n\")\n", moduleName)
		fmt.Fprintln(file, "        printf(\"加载命令: insmod tacosys.ko\\n\")")
		fmt.Fprintln(file, "    }")
		fmt.Fprintln(file, "    ")
		fmt.Fprintln(file, "    # 检查调试信息")
		fmt.Fprintln(file, "    printf(\"检查调试信息...\\n\")")
		fmt.Fprintln(file, "    system(\"find /lib/modules/$(uname -r) -name '*.ko' -exec file {} \\; | head -3\")")
		fmt.Fprintln(file, "}")
		fmt.Fprintln(file, "")
	}
}

// 写入断点探针
func writeBreakpointProbes(file *os.File, ctx *DebuggerContext) {
	fmt.Fprintln(file, "# 断点探针")
	fmt.Fprintln(file, "")

	// 检测项目类型
	isKernel, moduleName := isKernelModule(ctx)
	
	validBreakpoints := 0
	for _, bp := range ctx.Project.Breakpoints {
		if !bp.Enabled {
			continue
		}

		funcName := bp.Function
		if funcName == "unknown" || funcName == "" {
			// 尝试重新解析函数名
			if parsedName := parseFunctionName(bp.File, bp.Line); parsedName != "" {
				funcName = parsedName
			} else {
				continue
			}
		}

		fileName := filepath.Base(bp.File)

		// 生成探针
		fmt.Fprintf(file, "# 断点 %d: %s:%d 在函数 %s\n", validBreakpoints+1, fileName, bp.Line, funcName)
		
		// 根据项目类型生成不同的探测点
		if isKernel {
			// 对于内核模块，使用module.function探测点
			fmt.Fprintf(file, "probe module(\"%s\").function(\"%s\") {\n", moduleName, funcName)
		} else {
			fmt.Fprintf(file, "probe process.function(\"%s\") {\n", funcName)
		}
		fmt.Fprintln(file, "    # 基础信息收集")
		fmt.Fprintln(file, "    current_pid = pid()")
		fmt.Fprintln(file, "    current_tid = tid()")
		fmt.Fprintln(file, "    current_time = gettimeofday_ns()")
		fmt.Fprintln(file, "    ")

		// 变量监控
		fmt.Fprintln(file, "    # 变量监控")
		fmt.Fprintln(file, "    variables_json = \"\"")

		// 尝试获取常见的局部变量
		commonVars := []string{"local_var", "counter", "temp", "i", "j", "result", "param", "value"}
		for i, varName := range commonVars {
			if i == 0 {
				fmt.Fprintf(file, "    # 尝试获取变量 %s\n", varName)
				fmt.Fprintf(file, "    if (@defined($%s)) {\n", varName)
				fmt.Fprintf(file, "        variables_json = sprintf(\"        {\\\"name\\\": \\\"%s\\\", \\\"type\\\": \\\"auto\\\", \\\"value\\\": %%d, \\\"raw_value\\\": \\\"0x%%08x\\\"}\", $%s, $%s)\n", varName, varName, varName)
				fmt.Fprintln(file, "    }")
			} else {
				fmt.Fprintf(file, "    if (@defined($%s)) {\n", varName)
				fmt.Fprintf(file, "        if (variables_json != \"\") variables_json = variables_json \",\"\n")
				fmt.Fprintf(file, "        variables_json = variables_json sprintf(\"        {\\\"name\\\": \\\"%s\\\", \\\"type\\\": \\\"auto\\\", \\\"value\\\": %%d, \\\"raw_value\\\": \\\"0x%%08x\\\"}\", $%s, $%s)\n", varName, varName, varName)
				fmt.Fprintln(file, "    }")
			}
		}

		fmt.Fprintln(file, "    ")
		fmt.Fprintln(file, "    # 输出调试事件")
		fmt.Fprintf(file, "    output_debug_event(%d, \"%s\", current_pid, current_tid, current_time, variables_json)\n", validBreakpoints+1, funcName)
		fmt.Fprintln(file, "    ")
		fmt.Fprintln(file, "    # 控制台输出")
		fmt.Fprintf(file, "    printf(\"[BREAKPOINT-%d] %s:%d in %s() PID=%%d TID=%%d\\n\", current_pid, current_tid)\n", validBreakpoints+1, fileName, bp.Line, funcName)
		fmt.Fprintln(file, "}")
		fmt.Fprintln(file, "")

		validBreakpoints++
	}

	if validBreakpoints == 0 {
		fmt.Fprintln(file, "# 没有有效的断点")
		fmt.Fprintln(file, "probe begin {")
		fmt.Fprintln(file, "    printf(\"Warning: No valid breakpoints found\\n\")")
		fmt.Fprintln(file, "    exit()")
		fmt.Fprintln(file, "}")
	}
}

// 写入脚本尾部
func writeSystemTapFooter(file *os.File) {
	fmt.Fprintln(file, "# 脚本开始和结束处理")
	fmt.Fprintln(file, "probe begin {")
	fmt.Fprintln(file, "    printf(\"SystemTap调试脚本开始运行...\\n\")")
	fmt.Fprintln(file, "    printf(\"会话ID: %s\\n\", session_id)")
	fmt.Fprintln(file, "    printf(\"JSON输出文件: %s\\n\", json_output_file)")
	fmt.Fprintln(file, "    init_json_output()")
	fmt.Fprintln(file, "}")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "probe end {")
	fmt.Fprintln(file, "    printf(\"SystemTap调试脚本结束运行...\\n\")")
	fmt.Fprintln(file, "    printf(\"总共记录了 %d 个调试事件\\n\", debug_events_count)")
	fmt.Fprintln(file, "    finalize_json_output()")
	fmt.Fprintln(file, "}")
	fmt.Fprintln(file, "")
	fmt.Fprintln(file, "# 信号处理")
	fmt.Fprintln(file, "probe signal.send {")
	fmt.Fprintln(file, "    if (sig_name == \"SIGINT\" || sig_name == \"SIGTERM\") {")
	fmt.Fprintln(file, "        printf(\"收到终止信号，正在保存数据...\\n\")")
	fmt.Fprintln(file, "        finalize_json_output()")
	fmt.Fprintln(file, "        exit()")
	fmt.Fprintln(file, "    }")
	fmt.Fprintln(file, "}")
}

// 生成执行脚本
func generateExecutionScript(ctx *DebuggerContext, scriptPath string) error {
	// 生成启动脚本
	startScript := filepath.Join(ctx.Project.RootPath, "start_debug.sh")
	startFile, err := os.Create(startScript)
	if err != nil {
		return err
	}
	defer startFile.Close()

	fmt.Fprintln(startFile, "#!/bin/bash")
	fmt.Fprintln(startFile, "# SystemTap调试脚本启动器")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "set -e")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "SCRIPT_DIR=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")\" && pwd)\"")
	fmt.Fprintln(startFile, "SYSTEMTAP_SCRIPT=\"$SCRIPT_DIR/debug_monitor.stp\"")
	fmt.Fprintln(startFile, "# 智能检测用户home目录")
	fmt.Fprintln(startFile, "if [ -z \"$HOME\" ]; then")
	fmt.Fprintln(startFile, "    echo \"错误: 无法获取用户home目录 ($HOME 环境变量为空)\"")
	fmt.Fprintln(startFile, "    exit 1")
	fmt.Fprintln(startFile, "fi")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 创建.systemtap目录")
	fmt.Fprintln(startFile, "SYSTEMTAP_DIR=\"$HOME/.systemtap\"")
	fmt.Fprintln(startFile, "if ! mkdir -p \"$SYSTEMTAP_DIR\"; then")
	fmt.Fprintln(startFile, "    echo \"错误: 无法创建SystemTap目录 $SYSTEMTAP_DIR\"")
	fmt.Fprintln(startFile, "    echo \"请检查文件系统权限或磁盘空间\"")
	fmt.Fprintln(startFile, "    exit 1")
	fmt.Fprintln(startFile, "fi")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "JSON_OUTPUT=\"$SYSTEMTAP_DIR/debug_session.json\"")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "echo \"SystemTap调试脚本启动器\"")
	fmt.Fprintln(startFile, "echo \"脚本路径: $SYSTEMTAP_SCRIPT\"")
	fmt.Fprintln(startFile, "echo \"JSON输出: $JSON_OUTPUT\"")
	fmt.Fprintln(startFile, "echo \"\"")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 检查SystemTap是否可用")
	fmt.Fprintln(startFile, "if ! command -v stap &> /dev/null; then")
	fmt.Fprintln(startFile, "    echo \"错误: SystemTap (stap) 未安装\"")
	fmt.Fprintln(startFile, "    echo \"请安装: sudo apt-get install systemtap\"")
	fmt.Fprintln(startFile, "    exit 1")
	fmt.Fprintln(startFile, "fi")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 检查权限")
	fmt.Fprintln(startFile, "if [ \"$EUID\" -ne 0 ]; then")
	fmt.Fprintln(startFile, "    echo \"提示: SystemTap通常需要root权限\"")
	fmt.Fprintln(startFile, "    echo \"如果遇到权限问题，请使用: sudo $0\"")
	fmt.Fprintln(startFile, "    echo \"\"")
	fmt.Fprintln(startFile, "fi")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 清理旧的JSON输出")
	fmt.Fprintln(startFile, "rm -f \"$JSON_OUTPUT\"")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 启动SystemTap")
	fmt.Fprintln(startFile, "echo \"启动SystemTap监控...\"")
	fmt.Fprintln(startFile, "echo \"按Ctrl+C停止监控\"")
	fmt.Fprintln(startFile, "echo \"\"")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 使用合适的SystemTap路径")
	fmt.Fprintln(startFile, "STAP_CMD=\"stap\"")
	fmt.Fprintln(startFile, "if [ -x \"/usr/local/systemtap/bin/stap\" ]; then")
	fmt.Fprintln(startFile, "    STAP_CMD=\"/usr/local/systemtap/bin/stap\"")
	fmt.Fprintln(startFile, "fi")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 设置清理函数")
	fmt.Fprintln(startFile, "cleanup() {")
	fmt.Fprintln(startFile, "    echo \"\"")
	fmt.Fprintln(startFile, "    echo \"正在停止SystemTap...\"")
	fmt.Fprintln(startFile, "    if [ -f \"$JSON_OUTPUT\" ]; then")
	fmt.Fprintln(startFile, "        echo \"调试数据已保存到: $JSON_OUTPUT\"")
	fmt.Fprintln(startFile, "        echo \"可以使用JSON查看器查看结果\"")
	fmt.Fprintln(startFile, "    fi")
	fmt.Fprintln(startFile, "    exit 0")
	fmt.Fprintln(startFile, "}")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 设置信号处理")
	fmt.Fprintln(startFile, "trap cleanup SIGINT SIGTERM")
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 运行SystemTap脚本")
	
	// 根据是否设置了自定义内核路径来决定SystemTap参数
	if ctx.KernelPath != "" {
		fmt.Fprintln(startFile, "# 使用自定义内核源码路径")
		fmt.Fprintf(startFile, "KERNEL_SRC=\"%s\"\n", ctx.KernelPath)
		fmt.Fprintln(startFile, "echo \"使用内核源码路径: $KERNEL_SRC\"")
		fmt.Fprintln(startFile, "")
		fmt.Fprintln(startFile, "# 检查内核调试文件")
		fmt.Fprintln(startFile, "if [ ! -f \"$KERNEL_SRC/vmlinux\" ]; then")
		fmt.Fprintln(startFile, "    echo \"警告: $KERNEL_SRC/vmlinux 不存在\"")
		fmt.Fprintln(startFile, "    echo \"SystemTap可能无法正确解析符号\"")
		fmt.Fprintln(startFile, "    echo \"\"")
		fmt.Fprintln(startFile, "fi")
		fmt.Fprintln(startFile, "")
		fmt.Fprintln(startFile, "# 运行SystemTap with custom kernel source")
		fmt.Fprintln(startFile, "\"$STAP_CMD\" -r \"$(uname -r)\" -k \"$KERNEL_SRC\" \"$SYSTEMTAP_SCRIPT\"")
	} else {
		fmt.Fprintln(startFile, "# 使用系统默认内核路径")
		fmt.Fprintln(startFile, "\"$STAP_CMD\" \"$SYSTEMTAP_SCRIPT\"")
	}
	fmt.Fprintln(startFile, "")
	fmt.Fprintln(startFile, "# 脚本正常结束")
	fmt.Fprintln(startFile, "cleanup")

	// 设置执行权限
	err = os.Chmod(startScript, 0755)
	if err != nil {
		return err
	}

	// 生成停止脚本
	stopScript := filepath.Join(ctx.Project.RootPath, "stop_debug.sh")
	stopFile, err := os.Create(stopScript)
	if err != nil {
		return err
	}
	defer stopFile.Close()

	fmt.Fprintln(stopFile, "#!/bin/bash")
	fmt.Fprintln(stopFile, "# SystemTap调试脚本停止器")
	fmt.Fprintln(stopFile, "")
	fmt.Fprintln(stopFile, "echo \"正在停止SystemTap调试脚本...\"")
	fmt.Fprintln(stopFile, "")
	fmt.Fprintln(stopFile, "# 查找并终止SystemTap进程")
	fmt.Fprintln(stopFile, "STAP_PIDS=$(pgrep -f \"stap.*debug_monitor.stp\")")
	fmt.Fprintln(stopFile, "if [ -n \"$STAP_PIDS\" ]; then")
	fmt.Fprintln(stopFile, "    echo \"找到SystemTap进程: $STAP_PIDS\"")
	fmt.Fprintln(stopFile, "    kill -TERM $STAP_PIDS")
	fmt.Fprintln(stopFile, "    sleep 2")
	fmt.Fprintln(stopFile, "    # 如果进程仍然存在，强制终止")
	fmt.Fprintln(stopFile, "    if pgrep -f \"stap.*debug_monitor.stp\" > /dev/null; then")
	fmt.Fprintln(stopFile, "        echo \"强制终止SystemTap进程...\"")
	fmt.Fprintln(stopFile, "        kill -KILL $STAP_PIDS")
	fmt.Fprintln(stopFile, "    fi")
	fmt.Fprintln(stopFile, "    echo \"SystemTap调试脚本已停止\"")
	fmt.Fprintln(stopFile, "else")
	fmt.Fprintln(stopFile, "    echo \"没有找到运行中的SystemTap调试脚本\"")
	fmt.Fprintln(stopFile, "fi")

	// 设置执行权限
	err = os.Chmod(stopScript, 0755)
	if err != nil {
		return err
	}

	return nil
}

// ================== 命令集成 ==================

// 生成SystemTap脚本命令
func generateSystemTapCommand(ctx *DebuggerContext, args string) []string {
	if ctx.Project == nil {
		return []string{
			"错误: 请先打开一个项目",
			"使用 'open <项目路径>' 命令打开项目",
		}
	}

	if len(ctx.Project.Breakpoints) == 0 {
		return []string{
			"错误: 没有设置断点",
			"请在代码视图中双击代码行设置断点",
		}
	}

	// 解析参数
	var outputFile string
	if args != "" {
		outputFile = args
	} else {
		// 获取默认的SystemTap JSON输出路径
		var err error
		outputFile, err = getSystemTapJSONPath()
		if err != nil {
			return []string{
				"错误: 无法获取SystemTap输出路径",
				fmt.Sprintf("详细错误: %v", err),
				"",
				"💡 解决方案:",
				"• 确保用户home目录存在且可写",
				"• 检查文件系统权限",
				"• 或使用自定义路径: stp /path/to/output.json",
			}
		}
	}

	// 生成SystemTap脚本
	err := generateSystemTapScript(ctx, outputFile)
	if err != nil {
		return []string{
			fmt.Sprintf("错误: 生成SystemTap脚本失败: %v", err),
		}
	}

	// 统计信息
	enabledCount := 0
	for _, bp := range ctx.Project.Breakpoints {
		if bp.Enabled {
			enabledCount++
		}
	}

	return []string{
		"✅ SystemTap调试脚本生成成功!",
		"",
		fmt.Sprintf("📊 统计信息:"),
		fmt.Sprintf("  总断点数: %d", len(ctx.Project.Breakpoints)),
		fmt.Sprintf("  启用断点: %d", enabledCount),
		fmt.Sprintf("  项目路径: %s", ctx.Project.RootPath),
		"",
		fmt.Sprintf("📁 生成的文件:"),
		fmt.Sprintf("  SystemTap脚本: debug_monitor.stp"),
		fmt.Sprintf("  启动脚本: start_debug.sh"),
		fmt.Sprintf("  停止脚本: stop_debug.sh"),
		fmt.Sprintf("  JSON输出: %s", getSystemTapJSONDisplayPath()),
		"",
		fmt.Sprintf("🚀 使用方法:"),
		fmt.Sprintf("  1. 运行: ./start_debug.sh"),
		fmt.Sprintf("  2. 触发断点（运行您的程序）"),
		fmt.Sprintf("  3. 停止: Ctrl+C 或 ./stop_debug.sh"),
		fmt.Sprintf("  4. 查看结果: cat %s", getSystemTapJSONDisplayPath()),
		"",
		fmt.Sprintf("💡 提示:"),
		fmt.Sprintf("  • SystemTap需要root权限运行"),
		fmt.Sprintf("  • 确保目标程序包含调试符号"),
		fmt.Sprintf("  • JSON文件包含完整的调试会话信息"),
	}
}

// 查看SystemTap脚本状态
func systemTapStatusCommand(ctx *DebuggerContext) []string {
	if ctx.Project == nil {
		return []string{"错误: 没有打开项目"}
	}

	scriptPath := filepath.Join(ctx.Project.RootPath, "debug_monitor.stp")
	startScript := filepath.Join(ctx.Project.RootPath, "start_debug.sh")
	
	// 获取SystemTap JSON输出路径
	jsonOutput, err := getSystemTapJSONPath()
	if err != nil {
		return []string{
			"错误: 无法获取SystemTap输出路径",
			fmt.Sprintf("详细错误: %v", err),
		}
	}

	output := []string{
		"SystemTap脚本状态:",
		"",
	}

	// 检查脚本文件
	if _, err := os.Stat(scriptPath); err == nil {
		output = append(output, "✅ SystemTap脚本: debug_monitor.stp (存在)")
	} else {
		output = append(output, "❌ SystemTap脚本: debug_monitor.stp (不存在)")
	}

	// 检查启动脚本
	if _, err := os.Stat(startScript); err == nil {
		output = append(output, "✅ 启动脚本: start_debug.sh (存在)")
	} else {
		output = append(output, "❌ 启动脚本: start_debug.sh (不存在)")
	}

	// 检查JSON输出
	displayPath := getSystemTapJSONDisplayPath()
	if _, err := os.Stat(jsonOutput); err == nil {
		output = append(output, fmt.Sprintf("✅ JSON输出: %s (存在)", displayPath))
	} else {
		output = append(output, fmt.Sprintf("⏳ JSON输出: %s (未生成)", displayPath))
	}

	// 检查SystemTap是否运行
	output = append(output, "")
	output = append(output, "进程状态:")

	// 这里可以添加检查SystemTap进程的逻辑

	return output
}

// 获取SystemTap目录路径（跨平台兼容）
func getSystemTapDir() (string, error) {
	// 首先尝试使用Go标准库获取用户home目录
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// 如果Go标准库失败，尝试使用环境变量
		homeDir = os.Getenv("HOME")
		if homeDir == "" {
			// 在Windows上尝试USERPROFILE
			homeDir = os.Getenv("USERPROFILE")
			if homeDir == "" {
				return "", fmt.Errorf("无法获取用户home目录: %v", err)
			}
		}
	}
	
	// 创建.systemtap目录
	systemtapDir := filepath.Join(homeDir, ".systemtap")
	if err := os.MkdirAll(systemtapDir, 0755); err != nil {
		return "", fmt.Errorf("无法创建SystemTap目录 %s: %v", systemtapDir, err)
	}
	
	return systemtapDir, nil
}

// 获取SystemTap JSON输出文件路径
func getSystemTapJSONPath() (string, error) {
	systemtapDir, err := getSystemTapDir()
	if err != nil {
		return "", err
	}
	
	return filepath.Join(systemtapDir, "debug_session.json"), nil
}

// 获取用户友好的路径显示
func getSystemTapJSONDisplayPath() string {
	// 使用波浪线表示法显示路径，更加用户友好
	return "~/.systemtap/debug_session.json"
}

// 设置内核源码路径命令
func setKernelPathCommand(ctx *DebuggerContext, args string) []string {
	if args == "" {
		// 显示当前设置
		if ctx.KernelPath == "" {
			return []string{
				"🔧 内核源码路径设置",
				"",
				"当前状态: 未设置（使用系统默认）",
				"系统默认: /lib/modules/$(uname -r)/build/",
				"",
				"💡 用法:",
				"  kernel-path /path/to/kernel/source  # 设置内核源码路径",
				"  kernel-path show                    # 显示当前设置",
				"  kernel-path clear                   # 清除自定义路径",
				"",
				"📁 常见路径:",
				"  • /usr/src/linux-*",
				"  • /opt/kernel-source",
				"  • ~/workspace/linux",
				"  • 您的交叉编译内核源码目录",
			}
		} else {
			return []string{
				"🔧 内核源码路径设置",
				"",
				fmt.Sprintf("当前路径: %s", ctx.KernelPath),
				"",
				"💡 用法:",
				"  kernel-path /new/path              # 更改路径",
				"  kernel-path clear                  # 清除自定义路径",
			}
		}
	}
	
	switch strings.ToLower(args) {
	case "show":
		// 显示详细信息
		output := []string{
			"🔧 内核源码路径配置详情",
			"",
		}
		
		if ctx.KernelPath == "" {
			output = append(output,
				"当前设置: 使用系统默认",
				fmt.Sprintf("系统路径: /lib/modules/%s/build/", getKernelVersion()),
			)
		} else {
			output = append(output,
				fmt.Sprintf("自定义路径: %s", ctx.KernelPath),
			)
			
			// 检查路径是否存在
			if _, err := os.Stat(ctx.KernelPath); err != nil {
				output = append(output, "⚠️  警告: 路径不存在或无法访问")
			} else {
				output = append(output, "✅ 路径验证: 可访问")
				
				// 检查重要文件
				vmlinuxPath := filepath.Join(ctx.KernelPath, "vmlinux")
				if _, err := os.Stat(vmlinuxPath); err == nil {
					output = append(output, "✅ vmlinux: 存在")
				} else {
					output = append(output, "❌ vmlinux: 不存在")
				}
				
				systemMapPath := filepath.Join(ctx.KernelPath, "System.map")
				if _, err := os.Stat(systemMapPath); err == nil {
					output = append(output, "✅ System.map: 存在")
				} else {
					output = append(output, "❌ System.map: 不存在")
				}
			}
		}
		
		return output
		
	case "clear":
		// 清除自定义路径
		ctx.KernelPath = ""
		return []string{
			"🔧 内核源码路径设置",
			"",
			"✅ 已清除自定义路径",
			"现在将使用系统默认路径",
		}
		
	default:
		// 设置新路径
		kernelPath := strings.TrimSpace(args)
		
		// 验证路径
		if !filepath.IsAbs(kernelPath) {
			return []string{
				"❌ 错误: 请提供绝对路径",
				"",
				"示例: kernel-path /usr/src/linux-5.15.0",
			}
		}
		
		// 检查路径是否存在
		if _, err := os.Stat(kernelPath); err != nil {
			return []string{
				"❌ 错误: 路径不存在或无法访问",
				fmt.Sprintf("路径: %s", kernelPath),
				fmt.Sprintf("错误: %v", err),
				"",
				"💡 建议:",
				"• 检查路径拼写",
				"• 确保有访问权限",
				"• 使用 'ls -la' 验证路径",
			}
		}
		
		// 设置路径
		ctx.KernelPath = kernelPath
		
		// 检查重要文件
		warnings := []string{}
		vmlinuxPath := filepath.Join(kernelPath, "vmlinux")
		if _, err := os.Stat(vmlinuxPath); err != nil {
			warnings = append(warnings, "⚠️  vmlinux 文件不存在")
		}
		
		systemMapPath := filepath.Join(kernelPath, "System.map")
		if _, err := os.Stat(systemMapPath); err != nil {
			warnings = append(warnings, "⚠️  System.map 文件不存在")
		}
		
		output := []string{
			"🔧 内核源码路径设置",
			"",
			"✅ 路径设置成功!",
			fmt.Sprintf("新路径: %s", kernelPath),
			"",
		}
		
		if len(warnings) > 0 {
			output = append(output, "⚠️  注意事项:")
			output = append(output, warnings...)
			output = append(output, "")
			output = append(output, "💡 确保内核编译时启用了调试信息:")
			output = append(output, "  CONFIG_DEBUG_INFO=y")
		} else {
			output = append(output, "✅ 调试文件检查通过")
		}
		
		return output
	}
}


