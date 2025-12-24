#!/usr/bin/env python3
"""
Code Synchronization Script
同步本地代码到远程服务器
"""

import paramiko
import os
import sys
import subprocess
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

# CloudLab配置
HOST = "amd025.utah.cloudlab.us"
USERNAME = "wucy"
KEY_PATH = os.path.expanduser("~/.ssh/id_rsa")
PASSPHRASE = "michael"

# 服务器配置：端口 -> (角色, 节点ID)
SERVER_CONFIG = {
    27010: ("client", None),      # 客户端
    27011: ("node", 0),           # 节点0
    27012: ("node", 1),           # 节点1
    27013: ("node", 2),           # 节点2
    27014: ("node", 3),           # 节点3
}

def sync_code_to_server(port, role, node_id=None, max_retries=3):
    """同步代码到指定服务器"""
    server_name = f"{role}" + (f"-{node_id}" if node_id is not None else "")
    print(f"[{server_name}] Starting code sync on port {port}...")
    
    for attempt in range(max_retries):
        if attempt > 0:
            print(f"[{server_name}] Retry attempt {attempt + 1}/{max_retries}...")
            time.sleep(2)  # Wait 2 seconds between retries
        
        client = paramiko.SSHClient()
        client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        
        try:
            # 连接服务器 - 优先使用ssh-agent
            try:
                # 首先尝试使用ssh-agent
                client.connect(
                    hostname=HOST,
                    port=port,
                    username=USERNAME,
                    timeout=10,
                    look_for_keys=True,
                    allow_agent=True,
                )
            except Exception:
                # 如果ssh-agent失败，尝试使用密钥文件
                if PASSPHRASE is not None:
                    pkey = paramiko.RSAKey.from_private_key_file(KEY_PATH, password=PASSPHRASE)
                    client.connect(
                        hostname=HOST,
                        port=port,
                        username=USERNAME,
                        pkey=pkey,
                        timeout=10,
                        look_for_keys=False,
                        allow_agent=False,
                    )
                else:
                    # 尝试无密码的密钥文件
                    try:
                        pkey = paramiko.RSAKey.from_private_key_file(KEY_PATH)
                        client.connect(
                            hostname=HOST,
                            port=port,
                            username=USERNAME,
                            pkey=pkey,
                            timeout=10,
                            look_for_keys=False,
                            allow_agent=False,
                        )
                    except paramiko.ssh_exception.PasswordRequiredException:
                        print(f"[{server_name}] SSH key is encrypted but no passphrase provided")
                        print(f"[{server_name}] Trying expect method...")
                        return sync_code_with_expect(port, role, node_id)
            
            # 代码同步命令
            sync_command = """
            # 检查并更新代码
            if [ -d 'pbft' ]; then
                echo "Repository exists, updating..."
                cd pbft
                git fetch origin
                git reset --hard origin/feature/carousel_implementation
                git clean -fd
                echo "Code updated successfully"
            else
                echo "Repository not found, cloning..."
                git clone -b feature/carousel_implementation https://github.com/Michael112233/pbft.git
                echo "Repository cloned successfully"
            fi
            
            cd pbft
            echo "Current commit:"
            git log --oneline -1
            echo "Repository status:"
            git status --porcelain
            """
            
            # 执行同步命令
            stdin, stdout, stderr = client.exec_command(sync_command, timeout=60)
            stdout_data = stdout.read().decode('utf-8', errors='ignore')
            stderr_data = stderr.read().decode('utf-8', errors='ignore')
            exit_status = stdout.channel.recv_exit_status()
            
            if exit_status == 0:
                print(f"[{server_name}] Code sync completed")
                print(f"[{server_name}] Output: {stdout_data}")
                return True
            else:
                print(f"[{server_name}] Code sync failed: {stderr_data}")
                return False
                
        except Exception as e:
            error_msg = str(e)
            if "No existing session" in error_msg:
                print(f"[{server_name}] Attempt {attempt + 1} failed: SSH connection failed - server may be down or unreachable")
            elif "Error reading SSH protocol banner" in error_msg:
                print(f"[{server_name}] Attempt {attempt + 1} failed: SSH protocol error - server may be overloaded or SSH service unstable")
            elif "Connection timed out" in error_msg:
                print(f"[{server_name}] Attempt {attempt + 1} failed: Connection timeout - server not responding")
            elif "Connection refused" in error_msg:
                print(f"[{server_name}] Attempt {attempt + 1} failed: Connection refused - server not accepting connections")
            elif "socket.timeout" in error_msg:
                print(f"[{server_name}] Attempt {attempt + 1} failed: Network timeout - connection took too long")
            else:
                print(f"[{server_name}] Attempt {attempt + 1} failed: {e}")
            
            # If this is the last attempt, return False
            if attempt == max_retries - 1:
                return False
                
        finally:
            try:
                client.close()
            except Exception:
                pass
    
    return False

def sync_code_with_expect(port, role, node_id=None):
    """使用expect方法同步代码到指定服务器"""
    server_name = f"{role}" + (f"-{node_id}" if node_id is not None else "")
    print(f"[{server_name}] Using expect method for code sync on port {port}...")
    
    try:
        # 代码同步命令
        sync_command = """
        # 检查并更新代码
        if [ -d 'pbft' ]; then
            echo "Repository exists, updating..."
            cd pbft
            git fetch origin
            git reset --hard origin/feature/carousel_implementation
            git clean -fd
            echo "Code updated successfully"
        else
            echo "Repository not found, cloning..."
            git clone -b feature/carousel_implementation https://github.com/Michael112233/pbft.git
            echo "Repository cloned successfully"
        fi
        
        cd pbft
        echo "Current commit:"
        git log --oneline -1
        echo "Repository status:"
        git status --porcelain
        """
        
        # 转义命令中的特殊字符，但保持bash语法正确
        escaped_command = sync_command.replace('"', '\\"').replace('$', '\\$').replace('`', '\\`').replace('\n', '; ')
        
        # 创建expect脚本
        expect_script = f'''#!/usr/bin/expect -f
set timeout 30
spawn ssh -i {KEY_PATH} -o ConnectTimeout=10 -o StrictHostKeyChecking=no -p {port} {USERNAME}@{HOST} "{escaped_command}"
expect {{
    "Enter passphrase for key" {{
        send "{PASSPHRASE}\\r"
        exp_continue
    }}
    "password:" {{
        send "{PASSPHRASE}\\r"
        exp_continue
    }}
    "Permission denied" {{
        puts "SSH connection failed: Permission denied"
        exit 1
    }}
    "Connection refused" {{
        puts "SSH connection failed: Connection refused"
        exit 1
    }}
    "Connection timed out" {{
        puts "SSH connection failed: Connection timed out"
        exit 1
    }}
    eof {{
        puts "SSH connection completed"
        exit 0
    }}
    timeout {{
        puts "SSH connection timed out"
        exit 1
    }}
}}'''
        
        # 写入临时expect脚本
        temp_script = f"/tmp/sync_expect_{port}.exp"
        with open(temp_script, 'w') as f:
            f.write(expect_script)
        os.chmod(temp_script, 0o755)
        
        # 执行expect脚本
        result = subprocess.run([temp_script], capture_output=True, text=True, timeout=60)
        
        # 清理临时文件
        os.remove(temp_script)
        
        if result.returncode == 0:
            print(f"[{server_name}] Code sync completed")
            print(f"[{server_name}] Output: {result.stdout}")
            return True
        else:
            print(f"[{server_name}] Code sync failed: {result.stderr}")
            return False
            
    except Exception as e:
        print(f"[{server_name}] Error: {e}")
        return False

def main():
    """主函数"""
    print("Starting code synchronization to all servers...")
    
    # 并行同步到所有服务器
    with ThreadPoolExecutor(max_workers=5) as executor:
        # 提交所有任务
        future_to_port = {}
        for port, (role, node_id) in SERVER_CONFIG.items():
            future = executor.submit(sync_code_to_server, port, role, node_id)
            future_to_port[future] = port
        
        # 收集结果
        results = {}
        for future in as_completed(future_to_port):
            port = future_to_port[future]
            try:
                success = future.result()
                results[port] = success
            except Exception as e:
                print(f"[Port {port}] Exception: {e}")
                results[port] = False
    
    # 打印总结
    print("\n" + "=" * 50)
    print("CODE SYNC SUMMARY:")
    print("=" * 50)
    
    success_count = 0
    for port, (role, node_id) in SERVER_CONFIG.items():
        server_name = f"{role}" + (f"-{node_id}" if node_id is not None else "")
        status = "SUCCESS" if results.get(port, False) else "FAILED"
        print(f"Port {port} ({server_name}): {status}")
        if results.get(port, False):
            success_count += 1
    
    print(f"\nOverall: {success_count}/{len(SERVER_CONFIG)} servers synced successfully")
    
    if success_count == len(SERVER_CONFIG):
        print("🎉 All servers synced successfully!")
    elif success_count >= 3:
        print("✅ Most servers synced successfully!")
    else:
        print("❌ Many servers failed to sync")

if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        print("\nSync interrupted by user")
        sys.exit(1)
    except Exception as e:
        print(f"Unexpected error: {e}")
        sys.exit(1)