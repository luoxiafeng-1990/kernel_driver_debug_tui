# RISC-V 内核调试器 TUI (debug-gocui)

基于 Go 语言 gocui 框架的现代化内核调试器终端用户界面，专为嵌入式 Linux 内核开发和调试而设计。

## 🎯 主要特性

### 🏗️ 多窗口 TUI 界面
- **文件浏览器**：项目文件树浏览，支持展开/折叠目录
- **寄存器视图**：CPU寄存器状态显示
- **变量视图**：局部变量和全局变量监控
- **调用栈视图**：函数调用栈跟踪
- **代码视图**：源代码显示，支持语法高亮和断点标记
- **内存视图**：内存转储和十六进制查看
- **命令窗口**：交互式命令输入，类似终端体验
- **状态栏**：实时显示调试器状态和操作提示

### 🎮 动态布局系统
- **窗口调整**：拖拽窗口边界调整大小
- **全屏模式**：F11键切换任意窗口全屏显示
- **弹出窗口**：断点管理、帮助信息等弹出式窗口
- **响应式设计**：自适应终端大小变化

### 🔍 智能断点管理
- **一键设置**：双击代码行或按回车键设置断点
- **函数解析**：自动解析C函数名，支持多种函数定义格式
- **断点持久化**：断点信息自动保存到`.debug_breakpoints.json`
- **状态切换**：支持断点启用/禁用状态切换
- **批量操作**：清除所有断点、断点查看等

### 🚀 eBPF 集成
- **代码生成**：基于断点自动生成完整BPF调试程序
- **交叉编译**：支持多架构编译（x86_64、ARM64、RISC-V64）
- **脚本生成**：自动生成加载/卸载脚本
- **内核探针**：使用kprobe技术进行函数级调试
- **调试输出**：集成trace_pipe输出查看

### 🔎 代码搜索功能
- **实时搜索**：Ctrl+F启动搜索模式
- **高亮显示**：匹配项高亮和结果统计
- **快速跳转**：F3/Shift+F3在匹配项间跳转
- **大小写不敏感**：智能搜索算法

### 🖱️ 鼠标支持
- **点击聚焦**：鼠标点击切换窗口焦点
- **滚轮滚动**：鼠标滚轮上下滚动内容
- **拖拽选择**：鼠标拖拽选择文本
- **双击操作**：双击设置断点
- **边界拖拽**：拖拽窗口边界调整布局

### 📁 项目管理
- **项目打开**：支持路径补全和空格路径
- **文件树构建**：递归构建项目文件结构
- **文件内容读取**：自动读取和缓存文件内容
- **多格式支持**：C/C++源码、头文件、汇编等

## 🛠️ 系统依赖

### 运行时依赖
```bash
# Go 开发环境
go version >= 1.13

# eBPF 编译工具链
sudo apt install clang llvm           # Ubuntu/Debian
sudo yum install clang llvm           # CentOS/RHEL

# BPF 工具
sudo apt install bpfcc-tools bpftrace # Ubuntu/Debian
sudo yum install bcc-tools bpftrace   # CentOS/RHEL

# 内核头文件
sudo apt install linux-headers-$(uname -r)  # Ubuntu/Debian
sudo yum install kernel-devel-$(uname -r)   # CentOS/RHEL
```

### 内核支持
- Linux 内核版本 >= 4.4（基本BPF支持）
- Linux 内核版本 >= 5.13（RISC-V BPF JIT支持）
- 启用CONFIG_BPF_SYSCALL、CONFIG_BPF_JIT等选项

## 🚀 快速开始

### 1. 环境准备
```bash
# 克隆项目
git clone <repository>
cd debug-gocui

# 安装依赖
go mod tidy

# 构建程序
go build -o debug-gocui main.go
```

### 2. 启动调试器
```bash
# 直接运行
./debug-gocui

# 或者使用go run
go run main.go
```

### 3. 调试工作流程
```bash
# 在调试器中执行以下命令序列：
open /path/to/kernel/driver    # 打开内核驱动项目
# 双击代码行设置断点
generate                       # 生成BPF调试代码
compile                        # 编译BPF程序（可选）

# 退出调试器，在终端中执行：
sudo ./load_debug_bpf.sh       # 加载BPF调试程序
sudo cat /sys/kernel/debug/tracing/trace_pipe  # 查看调试输出
sudo ./unload_debug_bpf.sh     # 卸载BPF程序
```

## ⌨️ 快捷键参考

### 全局快捷键
| 快捷键 | 功能 |
|--------|------|
| `Tab` | 切换到下一个窗口 |
| `` ` `` | 切换到上一个窗口 |
| `F1-F6` | 直接切换到指定窗口 |
| `F11` | 切换全屏模式 |
| `ESC` | 退出全屏/关闭弹出窗口 |
| `PgUp/PgDn` | 上下翻页 |
| `Ctrl+C` | 退出程序 |
| `Ctrl+R` | 重置窗口布局 |

### 调试快捷键
| 快捷键 | 功能 |
|--------|------|
| `Enter` | 设置/切换断点（代码视图） |
| `g` | 生成BPF代码 |
| `c` | 清除所有断点 |
| `Ctrl+F` | 启动搜索模式 |
| `F3` | 跳转到下一个搜索结果 |
| `Shift+F3` | 跳转到上一个搜索结果 |

### 布局调整快捷键
| 快捷键 | 功能 |
|--------|------|
| `Ctrl+L` | 增加左侧面板宽度 |
| `Ctrl+Shift+L` | 减少左侧面板宽度 |
| `Ctrl+J` | 增加命令窗口高度 |
| `Ctrl+Shift+J` | 减少命令窗口高度 |

## 📝 命令参考

### 基本命令
```bash
help                    # 显示帮助信息
clear                   # 清屏
pwd                     # 显示当前工作目录
open <path>             # 打开项目目录
close                   # 关闭当前项目
```

### 断点命令
```bash
bp                      # 查看断点列表（弹出窗口）
bp clear                # 清除所有断点
breakpoint             # 清除所有断点（别名）
breakpoints            # 查看断点列表（别名）
```

### eBPF 命令
```bash
generate               # 生成BPF调试代码和脚本
compile                # 编译BPF代码
build                  # 编译BPF代码（别名）
```

### 状态命令
```bash
status                 # 显示调试器状态
```

## 🏗️ eBPF 调试原理

### 1. 断点到探针映射
- 解析C源码中的函数定义
- 将代码行断点映射到函数入口
- 生成对应的kprobe探针

### 2. BPF 程序结构
```c
// 自动生成的BPF程序示例
SEC("kprobe/target_function")
int trace_breakpoint_0(struct pt_regs *ctx) {
    // 获取进程信息
    u64 pid_tgid = bpf_get_current_pid_tgid();
    
    // 打印调试信息
    bpf_printk("[BREAKPOINT] function:%s PID:%d\n", 
               "target_function", pid_tgid);
    
    return 0;
}
```

### 3. 跨架构支持
- **编译目标**：BPF虚拟机字节码（平台无关）
- **JIT编译**：内核运行时编译为目标架构机器码
- **支持架构**：x86_64、ARM64、RISC-V64等

## 🎨 界面截图

```
┌────────────────────────────────────────────────────────────────────────────────┐
│ RISC-V Kernel Debugger | State: STOP | Func: main | Addr: 0x400000            │
├──────────────┬─────────────────────────────────────────────────┬───────────────┤
│ File Browser │ Code View                                       │ Registers     │
│              │                                                 │               │
│ ├─ src/      │ 1: #include <linux/module.h>                   │ RAX: 0x0      │
│ │  ├─ main.c │ 2: #include <linux/kernel.h>                   │ RBX: 0x0      │
│ │  └─ util.c │ 3:                                              │ RCX: 0x0      │
│ └─ include/  │ 4: static int __init driver_init(void)          │               │
│              │ 5: {                                            │               │
│              │ 6: ► printk("Driver loaded\n");                 │               │
│              │ 7:   return 0;                                  │               │
│              │ 8: }                                            │               │
├──────────────┼─────────────────────────────────────────────────┼───────────────┤
│ Command      │                                                 │ Variables     │
│              │                                                 │               │
│ > open /path │                                                 │ local_var: 42 │
│ > generate   │                                                 │               │
│ > compile    │                                                 │               │
│ BPF loaded   │                                                 │               │
└──────────────┴─────────────────────────────────────────────────┴───────────────┘
```

## 🔧 高级功能

### 1. 弹出窗口系统
- 断点管理器：查看和管理所有断点
- 帮助系统：完整的使用文档
- 支持窗口拖拽和滚动

### 2. 文本选择和复制
- 鼠标拖拽选择文本
- 支持跨行选择
- 剪贴板集成（OSC52协议）

### 3. 搜索系统
- 实时搜索反馈
- 正则表达式支持
- 搜索结果高亮显示

### 4. 响应式布局
- 自适应终端大小
- 动态调整窗口比例
- 保持最佳显示效果

## 🐛 故障排除

### 常见问题
1. **BPF编译失败**
   - 检查clang版本（推荐10+）
   - 确认安装了linux-headers
   - 验证内核BPF支持

2. **断点无法设置**
   - 确认项目路径正确
   - 检查源码格式是否为C/C++
   - 验证函数名解析是否正确

3. **eBPF加载失败**
   - 确认root权限
   - 检查内核配置
   - 验证目标函数是否存在

### 调试信息
```bash
# 检查内核BPF支持
cat /proc/sys/kernel/bpf_stats_enabled

# 查看BPF程序状态
sudo bpftool prog list

# 查看调试输出
sudo cat /sys/kernel/debug/tracing/trace_pipe
```

## 🤝 贡献指南

欢迎提交Issue和Pull Request！

### 开发环境
```bash
# 安装开发依赖
go mod tidy

# 运行测试
go test ./...

# 格式化代码
go fmt ./...

# 静态分析
go vet ./...
```

## 📄 许可证

本项目采用 MIT 许可证。

## 📞 联系方式

如有问题或建议，请提交 Issue 或联系开发者。

---

**注意**：此工具需要root权限来加载eBPF程序。请确保在安全的环境中使用，并遵守相关法律法规。
