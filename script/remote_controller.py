import paramiko
import os   

# ssh -p 28611 wucy@amd008.utah.cloudlab.us
host = "amd008.utah.cloudlab.us"
ports = [28610, 28611, 28612, 28613, 28614]
username = "wucy"
key_path = os.path.expanduser("~/.ssh/id_rsa")
passphrase = os.environ.get("SSH_KEY_PASSPHRASE")

for port in ports:
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        # First try ssh-agent/default keys to avoid bcrypt dependency on encrypted key parsing
        client.connect(
            hostname=host,
            port=port,
            username=username,
            timeout=10,
            look_for_keys=True,
            allow_agent=True,
        )
    except Exception:
        try:
            # Fallback to explicit key; use passphrase from env if provided
            pkey = paramiko.RSAKey.from_private_key_file(key_path, password=passphrase)
            client.connect(
                hostname=host,
                port=port,
                username=username,
                pkey=pkey,
                timeout=10,
                look_for_keys=False,
                allow_agent=False,
            )
        except Exception as e:
            print(f"connect {host}:{port} failed: {e}")
            try:
                client.close()
            except Exception:
                pass
            continue
    try:
        stdin, stdout, stderr = client.exec_command("git pull origin main")
        print(stdout.read().decode())
    finally:
        try:
            client.close()
        except Exception:
            pass



