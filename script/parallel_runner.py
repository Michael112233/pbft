#!/usr/bin/env python3
"""
Parallel PBFT Node Runner
同时控制多个CloudLab服务器并执行对应的PBFT节点命令
"""

import paramiko
import os
import getpass
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
import sys

# CloudLab配置
HOST = "amd025.utah.cloudlab.us"
USERNAME = "wucy"
KEY_PATH = os.path.expanduser("~/.ssh/id_rsa")
PASSPHRASE = "michael"

# 服务器配置：端口 -> (角色, 节点ID)
SERVER_CONFIG = {
    27010: ("client", None),      # 客户端
    27011: ("node", 0),          # 节点0
    27012: ("node", 1),          # 节点1  
    27013: ("node", 2),          # 节点2
    27014: ("node", 3),          # 节点3
}

# 如果环境变量中没有密码，尝试检测并提示输入
if PASSPHRASE is None and os.path.exists(KEY_PATH):
    try:
        # 测试密钥是否加密
        paramiko.RSAKey.from_private_key_file(KEY_PATH)
    except paramiko.ssh_exception.PasswordRequiredException:
        if sys.stdin.isatty():  # 只在交互式终端中提示
            PASSPHRASE = getpass.getpass("Enter SSH key passphrase: ")
        else:
            print("ERROR: SSH key is encrypted but no passphrase provided.")
            print("Please set SSH_KEY_PASSPHRASE environment variable or use ssh-agent.")
            sys.exit(1)

class ServerController:
    def __init__(self, port, role, node_id=None):
        self.port = port
        self.role = role
        self.node_id = node_id
        self.host = HOST
        self.username = USERNAME
        self.client = None
        
    def connect(self):
        """建立SSH连接"""
        self.client = paramiko.SSHClient()
        self.client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        
        try:
            # 首先尝试使用ssh-agent
            self.client.connect(
                hostname=self.host,
                port=self.port,
                username=self.username,
                timeout=10,
                look_for_keys=True,
                allow_agent=True,
            )
            return True
        except Exception:
            try:
                # 备用方案：使用明确的密钥文件
                if PASSPHRASE is not None:
                    pkey = paramiko.RSAKey.from_private_key_file(KEY_PATH, password=PASSPHRASE)
                else:
                    pkey = paramiko.RSAKey.from_private_key_file(KEY_PATH)
                    
                self.client.connect(
                    hostname=self.host,
                    port=self.port,
                    username=self.username,
                    pkey=pkey,
                    timeout=10,
                    look_for_keys=False,
                    allow_agent=False,
                )
                return True
            except Exception as e:
                print(f"[Port {self.port}] Connection failed: {e}")
                return False
    
    def execute_command(self, command):
        """执行远程命令"""
        if not self.client:
            return False, "No connection established"
            
        try:
            stdin, stdout, stderr = self.client.exec_command(command, timeout=300)  # 5分钟超时
            stdout_data = stdout.read().decode('utf-8', errors='ignore')
            stderr_data = stderr.read().decode('utf-8', errors='ignore')
            exit_status = stdout.channel.recv_exit_status()
            
            return exit_status == 0, {
                'stdout': stdout_data,
                'stderr': stderr_data,
                'exit_status': exit_status
            }
        except Exception as e:
            return False, f"Command execution failed: {type(e).__name__}: {e}"
    
    def setup_environment(self):
        """设置服务器环境"""
        server_name = f"{self.role}" + (f"-{self.node_id}" if self.node_id is not None else "")
        print(f"[{server_name}] Setting up environment on port {self.port}...")
        
        # 连接到服务器
        if not self.connect():
            print(f"[{server_name}] Failed to connect for environment setup")
            return False
            
        try:
            # 环境设置命令
            setup_commands = [
                "sudo apt-get update",
                "sudo apt-get install -y python3 python3-pip",
                "pip install requests",
                "wget https://go.dev/dl/go1.23.0.linux-amd64.tar.gz",
                "sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz",
                "echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc",
                "echo 'export GOPATH=$HOME/go' >> ~/.bashrc", 
                "echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc",
                "rm go1.23.0.linux-amd64.tar.gz",
                "source ~/.bashrc",
                "/usr/local/go/bin/go version",
                "cd pbft && chmod +x remote_run_linux.sh",
                "cd pbft && chmod +x run_project_linux.sh"
            ]
            
            print(f"[{server_name}] Executing environment setup commands...")
            
            # 执行所有环境设置命令
            for i, command in enumerate(setup_commands, 1):
                print(f"[{server_name}] Step {i}/{len(setup_commands)}: {command}")
                success, result = self.execute_command(command)
                
                if not success:
                    print(f"[{server_name}] Environment setup failed at step {i}")
                    if isinstance(result, dict) and result.get('stderr'):
                        print(f"[{server_name}] STDERR:\n{result['stderr']}")
                    else:
                        print(f"[{server_name}] Error: {result}")
                    return False
                else:
                    print(f"[{server_name}] Step {i} completed successfully")
            
            print(f"[{server_name}] Environment setup completed successfully")
            return True
            
        except Exception as e:
            print(f"[{server_name}] Environment setup error: {e}")
            return False
        finally:
            self.close()
    
    def run_pbft_node(self):
        """运行PBFT节点"""
        server_name = f"{self.role}" + (f"-{self.node_id}" if self.node_id is not None else "")
        print(f"[{server_name}] Starting on port {self.port}...")
        
        # 连接到服务器
        if not self.connect():
            print(f"[{server_name}] Failed to connect")
            return False
            
        try:
            # 构建命令，确保使用正确的环境变量，并在后台运行
            if self.role == "client":
                command = "cd pbft && export PATH=$PATH:/usr/local/go/bin && export GOPATH=$HOME/go && ./remote_run_linux.sh --role client --background"
            else:
                command = f"cd pbft && export PATH=$PATH:/usr/local/go/bin && export GOPATH=$HOME/go && ./remote_run_linux.sh --role node --node-id {self.node_id} --background"
            
            print(f"[{server_name}] Executing: {command}")
            
            # 执行命令
            success, result = self.execute_command(command)
            
            if success:
                print(f"[{server_name}] Command completed successfully")
                if isinstance(result, dict) and result.get('stdout'):
                    print(f"[{server_name}] STDOUT:\n{result['stdout']}")
            else:
                if isinstance(result, dict):
                    print(f"[{server_name}] Command failed (exit code: {result.get('exit_status', 'unknown')})")
                    if result.get('stderr'):
                        print(f"[{server_name}] STDERR:\n{result['stderr']}")
                else:
                    print(f"[{server_name}] Command failed: {result}")
                    
            return success
            
        except Exception as e:
            print(f"[{server_name}] Error: {e}")
            return False
        finally:
            self.close()
    
    def close(self):
        """关闭连接"""
        if self.client:
            try:
                self.client.close()
            except:
                pass

def run_parallel_pbft():
    """并行运行所有PBFT节点和客户端，先设置环境，然后客户端延迟5秒启动"""
    print("Starting parallel PBFT execution...")
    print(f"Target servers: {len(SERVER_CONFIG)} instances")
    
    # 创建所有控制器
    all_controllers = []
    node_controllers = []
    client_controllers = []
    
    for port, (role, node_id) in SERVER_CONFIG.items():
        controller = ServerController(port, role, node_id)
        all_controllers.append(controller)
        if role == "client":
            client_controllers.append(controller)
        else:
            node_controllers.append(controller)
    
    # 第一步：并行设置所有服务器的环境
    print("\n" + "="*50)
    print("STEP 1: ENVIRONMENT SETUP")
    print("="*50)
    
    setup_results = {}
    with ThreadPoolExecutor(max_workers=len(SERVER_CONFIG)) as executor:
        setup_futures = {
            executor.submit(controller.setup_environment): controller 
            for controller in all_controllers
        }
        
        for future in as_completed(setup_futures.keys()):
            controller = setup_futures[future]
            server_name = f"{controller.role}" + (f"-{controller.node_id}" if controller.node_id is not None else "")
            
            try:
                success = future.result()
                setup_results[controller.port] = success
                status = "SUCCESS" if success else "FAILED"
                print(f"[{server_name}] Environment setup: {status}")
            except Exception as e:
                setup_results[controller.port] = False
                print(f"[{server_name}] Environment setup exception: {e}")
    
    # 检查环境设置结果
    setup_success_count = sum(1 for success in setup_results.values() if success)
    print(f"\nEnvironment setup: {setup_success_count}/{len(SERVER_CONFIG)} servers completed successfully")
    
    if setup_success_count < len(SERVER_CONFIG):
        print("❌ Some servers failed environment setup. Continuing with successful ones...")
    
    # 第二步：运行PBFT节点和客户端
    print("\n" + "="*50)
    print("STEP 2: PBFT EXECUTION")
    print("="*50)
    
    results = {}
    
    # 使用线程池同时处理节点和客户端
    with ThreadPoolExecutor(max_workers=len(SERVER_CONFIG)) as executor:
        # 立即启动所有节点
        print("Starting all PBFT nodes...")
        node_futures = {
            executor.submit(controller.run_pbft_node): controller 
            for controller in node_controllers
            if setup_results.get(controller.port, False)  # 只运行环境设置成功的节点
        }
        
        # 延迟5秒后启动客户端
        def delayed_client_start():
            time.sleep(5)
            print("5 seconds elapsed, starting client...")
            if client_controllers and setup_results.get(client_controllers[0].port, False):
                return client_controllers[0].run_pbft_node()
            return True
        
        client_future = None
        if client_controllers and setup_results.get(client_controllers[0].port, False):
            client_future = executor.submit(delayed_client_start)
        
        # 收集节点结果
        for future in as_completed(node_futures.keys()):
            controller = node_futures[future]
            server_name = f"{controller.role}" + (f"-{controller.node_id}" if controller.node_id is not None else "")
            
            try:
                success = future.result()
                results[controller.port] = success
                status = "SUCCESS" if success else "FAILED"
                print(f"[{server_name}] Final status: {status}")
            except Exception as e:
                results[controller.port] = False
                print(f"[{server_name}] Exception: {e}")
        
        # 收集客户端结果
        if client_future:
            controller = client_controllers[0]
            server_name = f"{controller.role}" + (f"-{controller.node_id}" if controller.node_id is not None else "")
            
            try:
                success = client_future.result()
                results[controller.port] = success
                status = "SUCCESS" if success else "FAILED"
                print(f"[{server_name}] Final status: {status}")
            except Exception as e:
                results[controller.port] = False
                print(f"[{server_name}] Exception: {e}")
    
    # 输出总结
    print("\n" + "="*50)
    print("FINAL EXECUTION SUMMARY:")
    print("="*50)
    
    success_count = sum(1 for success in results.values() if success)
    total_count = len(results)
    
    for port, (role, node_id) in SERVER_CONFIG.items():
        server_name = f"{role}" + (f"-{node_id}" if node_id is not None else "")
        setup_status = "SUCCESS" if setup_results.get(port, False) else "FAILED"
        exec_status = "SUCCESS" if results.get(port, False) else "FAILED"
        print(f"Port {port} ({server_name}): Setup={setup_status}, Execution={exec_status}")
    
    print(f"\nEnvironment Setup: {setup_success_count}/{len(SERVER_CONFIG)} servers successful")
    print(f"PBFT Execution: {success_count}/{total_count} servers successful")
    
    return success_count == total_count

if __name__ == "__main__":
    try:
        success = run_parallel_pbft()
        sys.exit(0 if success else 1)
    except KeyboardInterrupt:
        print("\nExecution interrupted by user")
        sys.exit(1)
    except Exception as e:
        print(f"Unexpected error: {e}")
        sys.exit(1)
