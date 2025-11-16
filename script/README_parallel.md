# PBFT 并行节点执行脚本

这里提供了两个脚本来同时控制多个CloudLab服务器并执行PBFT节点：

## 脚本说明

### 1. `parallel_runner.py` (Python版本)
- **功能**: 使用Python和paramiko库并行连接所有服务器
- **特点**: 详细的错误处理、实时输出、连接状态监控
- **适用**: 需要详细日志和错误诊断的场景

### 2. `run_all_nodes.sh` (Bash版本)  
- **功能**: 使用bash和SSH并行执行命令
- **特点**: 简单快速、依赖系统SSH客户端
- **适用**: 快速启动和简单场景

## 服务器配置

两个脚本都会连接以下服务器并执行对应命令：

| 端口  | 角色   | 执行命令 |
|-------|--------|----------|
| 25610 | client | `./remote_run_linux.sh --role client` |
| 25611 | node-0 | `./remote_run_linux.sh --role node --node-id 0` |
| 25612 | node-1 | `./remote_run_linux.sh --role node --node-id 1` |
| 25613 | node-2 | `./remote_run_linux.sh --role node --node-id 2` |
| 25614 | node-3 | `./remote_run_linux.sh --role node --node-id 3` |

## 使用方法

### 方法1: 使用Python脚本

```bash
# 设置SSH密钥密码（如果需要）
export SSH_KEY_PASSPHRASE='your_passphrase'

# 运行脚本
python3 script/parallel_runner.py
```

### 方法2: 使用Bash脚本

```bash
# 直接运行（推荐）
./script/run_all_nodes.sh

# 或者
bash script/run_all_nodes.sh
```

### 方法3: 使用ssh-agent（推荐）

```bash
# 启动ssh-agent并添加密钥
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_rsa

# 然后运行任一脚本
./script/run_all_nodes.sh
# 或
python3 script/parallel_runner.py
```

## 启动顺序

**重要**: 脚本采用并行启动模式，节点和客户端同时运行：

1. **立即启动**: 所有PBFT节点 (node-0 到 node-3) 立即开始执行
2. **延迟5秒**: 客户端在节点启动5秒后开始执行
3. **并行运行**: 节点和客户端同时运行，直到各自完成

这种启动方式确保节点有足够时间初始化网络连接，同时不需要等待节点完全执行完毕。

## 输出说明

### Python脚本输出
```
Starting parallel PBFT execution...
Target servers: 5 instances
Starting all PBFT nodes...
[node-0] Starting on port 25611...
[node-1] Starting on port 25612...
[node-2] Starting on port 25613...
[node-3] Starting on port 25614...
5 seconds elapsed, starting client...
[client] Starting on port 25610...
[node-0] Final status: SUCCESS
[node-1] Final status: SUCCESS
[node-2] Final status: SUCCESS
[node-3] Final status: SUCCESS
[client] Final status: SUCCESS
==================================================
EXECUTION SUMMARY:
==================================================
Port 25610 (client): SUCCESS
Port 25611 (node-0): SUCCESS
Port 25612 (node-1): SUCCESS
Port 25613 (node-2): SUCCESS
Port 25614 (node-3): SUCCESS

Overall: 5/5 servers completed successfully
```

### Bash脚本输出
```
Starting PBFT nodes on all servers...
Starting all PBFT nodes...
[Port 25611] Starting node --node-id 0 immediately...
[Port 25612] Starting node --node-id 1 immediately...
[Port 25613] Starting node --node-id 2 immediately...
[Port 25614] Starting node --node-id 3 immediately...
All processes started. Waiting for completion...
5 seconds elapsed, starting client...
[Port 25610] Starting client...
==========================================
EXECUTION SUMMARY
==========================================
Port 25610 (client): SUCCESS
Port 25611 (node --node-id 0): SUCCESS
Port 25612 (node --node-id 1): SUCCESS
Port 25613 (node --node-id 2): SUCCESS
Port 25614 (node --node-id 3): SUCCESS

Successful: 5/5
Logs available in: ./parallel_logs/
```

## 日志文件

### Python脚本
- 实时输出到控制台
- 错误信息直接显示

### Bash脚本  
- 每个服务器的输出保存到 `./parallel_logs/server_<port>.log`
- 可以查看具体的执行日志：
  ```bash
  # 查看客户端日志
  cat ./parallel_logs/server_25610.log
  
  # 查看节点0日志
  cat ./parallel_logs/server_25611.log
  ```

## 故障排除

### 1. SSH连接失败
```bash
# 检查SSH密钥
ssh-add -l

# 手动测试连接
ssh -p 25610 wucy@c220g2-010811.wisc.cloudlab.us echo "test"
```

### 2. 密钥加密问题
```bash
# 方案1: 设置环境变量
export SSH_KEY_PASSPHRASE='your_passphrase'

# 方案2: 使用ssh-agent
ssh-add ~/.ssh/id_rsa
```

### 3. 权限问题
```bash
# 确保脚本有执行权限
chmod +x script/parallel_runner.py
chmod +x script/run_all_nodes.sh
```

### 4. 依赖问题
```bash
# Python脚本需要paramiko
pip install paramiko

# Bash脚本只需要系统SSH客户端
which ssh
```

## 环境变量

可以通过环境变量自定义配置：

```bash
# SSH密钥密码
export SSH_KEY_PASSPHRASE='your_passphrase'

# 仓库URL（用于remote_controller.py）
export REPO_URL='https://github.com/your_username/pbft.git'

# 分支名称
export BRANCH='main'
```

## 注意事项

1. **启动顺序**: 节点立即启动，客户端延迟5秒启动，然后并行运行
2. **网络初始化**: 5秒延迟确保PBFT节点有时间初始化网络连接
3. **超时设置**: 连接超时设为10秒，命令超时设为30秒
4. **错误处理**: 单个服务器失败不会影响其他服务器
5. **日志管理**: 定期清理日志文件避免磁盘空间不足
6. **网络稳定**: 确保到CloudLab的网络连接稳定
7. **资源要求**: 确保所有CloudLab节点有足够的CPU和内存资源

## 快速开始

最简单的使用方式：

```bash
# 1. 添加SSH密钥到agent
ssh-add ~/.ssh/id_rsa

# 2. 运行bash脚本
./script/run_all_nodes.sh

# 3. 查看结果
echo "Exit code: $?"
```
