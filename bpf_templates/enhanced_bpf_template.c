#include <linux/bpf.h>
#include <linux/ptrace.h>
#include <linux/types.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

// 定义目标架构（RISC-V）
#ifndef __TARGET_ARCH_riscv
#define __TARGET_ARCH_riscv
#endif

// 确保类型定义
typedef __u32 u32;
typedef __u64 u64;

// 增强版调试事件结构（与main.go中BPFDebugEvent结构保持一致）
struct enhanced_debug_event {
    u32 pid;
    u32 tgid;
    u64 timestamp;
    u32 breakpoint_id;
    char comm[16];
    char function[64];
    
    // RISC-V寄存器状态
    u64 pc, ra, sp, gp, tp;
    u64 t0, t1, t2;
    u64 s0, s1;
    u64 a0, a1, a2, a3, a4, a5, a6, a7;
    
    // 栈数据和局部变量
    u64 stack_data[8];
    u64 local_vars[16];
};

// 使用环形缓冲区传输数据而不是printk
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 1 << 24);
} rb SEC(".maps");

// 控制映射 - 用于启用/禁用调试
struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, u32);
    __type(value, u32);
} debug_control SEC(".maps");

// 辅助函数：读取用户空间栈数据
static __always_inline int read_user_stack_data(struct pt_regs *ctx, u64 *stack_data, int count) {
    u64 sp;
    int i;
    
    // 获取栈指针
    sp = PT_REGS_SP(ctx);
    
    // 读取栈数据
    for (i = 0; i < count && i < 8; i++) {
        if (bpf_probe_read_user(&stack_data[i], sizeof(u64), (void *)(sp + i * 8)) != 0) {
            stack_data[i] = 0; // 读取失败则设为0
        }
    }
    
    return 0;
}

// 辅助函数：尝试读取局部变量（通过栈帧分析）
static __always_inline int read_local_variables(struct pt_regs *ctx, u64 *local_vars, int count) {
    u64 fp;  // 帧指针
    int i;
    
    // 获取帧指针（RISC-V中通常是s0寄存器）
    fp = PT_REGS_FP(ctx);
    
    // 读取帧指针附近的数据作为潜在的局部变量
    for (i = 0; i < count && i < 16; i++) {
        if (bpf_probe_read_user(&local_vars[i], sizeof(u64), (void *)(fp - (i + 1) * 8)) != 0) {
            local_vars[i] = 0; // 读取失败则设为0
        }
    }
    
    return 0;
}

// 增强版断点探针模板
SEC("kprobe/target_function")
int enhanced_breakpoint_probe(struct pt_regs *ctx) {
    struct enhanced_debug_event *event;
    u32 key = 0;
    u32 *enabled;
    
    // 检查调试是否启用
    enabled = bpf_map_lookup_elem(&debug_control, &key);
    if (!enabled || *enabled == 0) {
        return 0;
    }
    
    // 分配环形缓冲区空间
    event = bpf_ringbuf_reserve(&rb, sizeof(*event), 0);
    if (!event) {
        return 0;
    }
    
    // 初始化事件结构
    __builtin_memset(event, 0, sizeof(*event));
    
    // 基本信息
    event->pid = bpf_get_current_pid_tgid() & 0xffffffff;
    event->tgid = bpf_get_current_pid_tgid() >> 32;
    event->timestamp = bpf_ktime_get_ns();
    event->breakpoint_id = 1; // 这里应该根据实际断点设置
    
    // 获取进程名
    bpf_get_current_comm(&event->comm, sizeof(event->comm));
    
    // 设置函数名（这里是模板，实际生成时会替换）
    bpf_probe_read_str(&event->function, sizeof(event->function), "target_function");
    
    // 读取RISC-V寄存器状态
    event->pc = PT_REGS_IP(ctx);     // 程序计数器
    event->ra = PT_REGS_RC(ctx);     // 返回地址  
    event->sp = PT_REGS_SP(ctx);     // 栈指针
    event->gp = PT_REGS_FP(ctx);     // 全局指针（近似）
    event->tp = 0;                   // 线程指针（难以直接获取）
    
    // 参数寄存器（RISC-V调用约定）
    event->a0 = PT_REGS_PARM1(ctx);
    event->a1 = PT_REGS_PARM2(ctx);
    event->a2 = PT_REGS_PARM3(ctx);
    event->a3 = PT_REGS_PARM4(ctx);
    event->a4 = PT_REGS_PARM5(ctx);
    event->a5 = PT_REGS_PARM6(ctx);
    // a6, a7通常需要从栈中读取
    event->a6 = 0;
    event->a7 = 0;
    
    // 临时寄存器和保存寄存器（难以直接获取，设为0）
    event->t0 = event->t1 = event->t2 = 0;
    event->s0 = event->s1 = 0;
    
    // 读取栈数据
    read_user_stack_data(ctx, event->stack_data, 8);
    
    // 尝试读取局部变量
    read_local_variables(ctx, event->local_vars, 16);
    
    // 提交事件到环形缓冲区
    bpf_ringbuf_submit(event, 0);
    
    // 同时输出到trace_pipe用于快速调试
    bpf_printk("[ENHANCED-BP] %s() PID=%d PC=0x%llx SP=0x%llx\n", 
               "target_function", event->pid, event->pc, event->sp);
    bpf_printk("[ARGS] a0=0x%llx a1=0x%llx a2=0x%llx a3=0x%llx\n",
               event->a0, event->a1, event->a2, event->a3);
    
    return 0;
}

// 添加函数返回探针
SEC("kretprobe/target_function")
int enhanced_trace_function_return(struct pt_regs *ctx) {
    u64 pid_tgid = bpf_get_current_pid_tgid();
    u32 pid = pid_tgid;
    u64 return_value = PT_REGS_RC(ctx);
    
    bpf_printk("[RETURN] target_function() PID=%d return=0x%llx\n", 
               pid, return_value);
    
    return 0;
}

char LICENSE[] SEC("license") = "GPL"; 