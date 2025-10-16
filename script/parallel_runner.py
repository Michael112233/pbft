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
HOST = "sm220u-10s10633.wisc.cloudlab.us"
USERNAME = "wucy"
KEY_PATH = os.path.expanduser("~/.ssh/id_rsa")
PASSPHRASE = os.environ.get("SSH_KEY_PASSPHRASE")

# 服务器配置：端口 -> (角色, 节点ID)
SERVER_CONFIG = {
    25410: ("client", None),      # 客户端
    25411: ("node", 0),          # 节点0
    25412: ("node", 1),          # 节点1  
    25413: ("node", 2),          # 节点2
    25414: ("node", 3),          # 节点3
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
            stdin, stdout, stderr = self.client.exec_command(command, timeout=30)
            stdout_data = stdout.read().decode('utf-8', errors='ignore')
            stderr_data = stderr.read().decode('utf-8', errors='ignore')
            exit_status = stdout.channel.recv_exit_status()
            
            return exit_status == 0, {
                'stdout': stdout_data,
                'stderr': stderr_data,
                'exit_status': exit_status
            }
        except Exception as e:
            return False, f"Command execution failed: {e}"
    
    def run_pbft_node(self):
        """运行PBFT节点"""
        server_name = f"{self.role}" + (f"-{self.node_id}" if self.node_id is not None else "")
        print(f"[{server_name}] Starting on port {self.port}...")
        
        # 连接到服务器
        if not self.connect():
            print(f"[{server_name}] Failed to connect")
            return False
            
        try:
            # 首先确保脚本有执行权限
            chmod_command = "cd pbft && chmod +x remote_run_linux.sh"
            print(f"[{server_name}] Setting execute permission: {chmod_command}")
            chmod_success, chmod_result = self.execute_command(chmod_command)
            
            if not chmod_success:
                print(f"[{server_name}] Failed to set execute permission: {chmod_result}")
                return False
            
            # 构建命令
            if self.role == "client":
                command = "cd pbft && ./remote_run_linux.sh --role client"
            else:
                command = f"cd pbft && ./remote_run_linux.sh --role node --node-id {self.node_id}"
            
            print(f"[{server_name}] Executing: {command}")
            
            # 执行命令
            success, result = self.execute_command(command)
            
            if success:
                print(f"[{server_name}] Command completed successfully")
                if result['stdout']:
                    print(f"[{server_name}] STDOUT:\n{result['stdout']}")
            else:
                print(f"[{server_name}] Command failed (exit code: {result.get('exit_status', 'unknown')})")
                if result.get('stderr'):
                    print(f"[{server_name}] STDERR:\n{result['stderr']}")
                    
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
    """并行运行所有PBFT节点和客户端，客户端延迟5秒启动"""
    print("Starting parallel PBFT execution...")
    print(f"Target servers: {len(SERVER_CONFIG)} instances")
    
    # 分离节点和客户端
    node_controllers = []
    client_controllers = []
    
    for port, (role, node_id) in SERVER_CONFIG.items():
        controller = ServerController(port, role, node_id)
        if role == "client":
            client_controllers.append(controller)
        else:
            node_controllers.append(controller)
    
    results = {}
    
    # 使用线程池同时处理节点和客户端
    with ThreadPoolExecutor(max_workers=len(SERVER_CONFIG)) as executor:
        # 立即启动所有节点
        print("Starting all PBFT nodes...")
        node_futures = {
            executor.submit(controller.run_pbft_node): controller 
            for controller in node_controllers
        }
        
        # 延迟5秒后启动客户端
        def delayed_client_start():
            time.sleep(5)
            print("5 seconds elapsed, starting client...")
            return client_controllers[0].run_pbft_node() if client_controllers else True
        
        client_future = None
        if client_controllers:
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
    print("EXECUTION SUMMARY:")
    print("="*50)
    
    success_count = sum(1 for success in results.values() if success)
    total_count = len(results)
    
    for port, (role, node_id) in SERVER_CONFIG.items():
        server_name = f"{role}" + (f"-{node_id}" if node_id is not None else "")
        status = "SUCCESS" if results.get(port, False) else "FAILED"
        print(f"Port {port} ({server_name}): {status}")
    
    print(f"\nOverall: {success_count}/{total_count} servers completed successfully")
    
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
