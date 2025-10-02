import paramiko
import os
import getpass

# ssh -p 25611 wucy@c220g2-010811.wisc.cloudlab.us
host = "c220g2-010811.wisc.cloudlab.us"
ports = [25610, 25611, 25612, 25613, 25614]
username = "wucy"
key_path = os.path.expanduser("~/.ssh/id_rsa")
passphrase = os.environ.get("SSH_KEY_PASSPHRASE")
REPO_URL = os.environ.get("REPO_URL", "https://github.com/Michael112233/pbft.git")
BRANCH = os.environ.get("BRANCH", "main")

# If passphrase not in environment, prompt for it
if passphrase is None and os.path.exists(key_path):
    try:
        # Test if key is encrypted by trying to load it without passphrase
        paramiko.RSAKey.from_private_key_file(key_path)
    except paramiko.ssh_exception.PasswordRequiredException:
        passphrase = getpass.getpass("Enter SSH key passphrase: ")

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
        remote_cmd = (
            f"if [ -d 'pbft' ]; then "
            f"cd pbft && git fetch origin {BRANCH} && git checkout {BRANCH} && git pull origin {BRANCH}; "
            f"else git clone -b {BRANCH} {REPO_URL}; fi"
        )
        stdin, stdout, stderr = client.exec_command(remote_cmd)
        print("STDOUT:", stdout.read().decode())
        print("STDERR:", stderr.read().decode())
        remote_cmd = (
            f"cd pbft && chmod +x remote_run_linux.sh &&"
            f"chmod +x script/environment_setup.sh &&"
            f"./script/environment_setup.sh"
        )
        stdin, stdout, stderr = client.exec_command(remote_cmd)
        print("STDOUT:", stdout.read().decode())
        print("STDERR:", stderr.read().decode())
    finally:
        try:
            client.close()
        except Exception:
            pass



