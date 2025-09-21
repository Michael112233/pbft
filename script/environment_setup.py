import platform
import subprocess
import os
import sys
import time

def is_linux():
    # Check if the system is Linux
    return platform.system().lower() == 'linux'

def check_go_installed():
    # Check if Go is installed and the version is correct
    try:
        result = subprocess.run(
            ["go", "version"], 
            capture_output=True, 
            text=True, 
            timeout=10
        )
        if result.returncode == 0 and "go version" in result.stdout:
            print(f"Go is already installed: {result.stdout.strip()}")
            return True
    except (subprocess.TimeoutExpired, FileNotFoundError):
        pass
    
    print("Go is not installed or not working properly")
    return False

def environment_setup():
    if not is_linux():
        print("This script is only for Linux systems")
        return False
    
    print("Setting up Linux environment...")
    
    try:
        # 1. Update package manager
        print("Updating package manager...")
        result = subprocess.run(
            ["sudo", "apt-get", "update"], 
            capture_output=True, 
            text=True, 
            timeout=300
        )
        if result.returncode != 0:
            print(f"Failed to update package manager: {result.stderr}")
            return False
        
        # 2. Install Python3 and pip
        print("Installing Python3 and pip...")
        result = subprocess.run(
            ["sudo", "apt-get", "install", "-y", "python3", "python3-pip"], 
            capture_output=True, 
            text=True, 
            timeout=300
        )
        if result.returncode != 0:
            print(f"Failed to install Python3/pip: {result.stderr}")
            return False
        
        # 3. Install requests library
        print("Installing requests library...")
        result = subprocess.run(
            ["pip3", "install", "requests"], 
            capture_output=True, 
            text=True, 
            timeout=120
        )
        if result.returncode != 0:
            print(f"Failed to install requests: {result.stderr}")
            return False
        
        # 4. Check and install Go
        if not check_go_installed():
            if not install_go():
                return False
        
        print("Linux environment setup completed successfully")
        return True
        
    except subprocess.TimeoutExpired:
        print("Linux environment setup timed out")
        return False
    except Exception as e:
        print(f"Linux environment setup failed: {e}")
        return False

def install_go():
    # Install Go
    print("Installing Go...")
    
    try:
        # 1. Download Go
        go_version = "go1.23.0"
        go_file = f"{go_version}.linux-amd64.tar.gz"
        go_url = f"https://go.dev/dl/{go_file}"
        
        print(f"Downloading {go_file}...")
        result = subprocess.run(
            ["wget", go_url], 
            capture_output=True, 
            text=True, 
            timeout=300
        )
        if result.returncode != 0:
            print(f"Failed to download Go: {result.stderr}")
            return False
        
        # 2. Delete old version and install new version
        print("Installing Go...")
        result = subprocess.run(
            ["sudo", "rm", "-rf", "/usr/local/go"], 
            capture_output=True, 
            text=True, 
            timeout=30
        )
        
        result = subprocess.run(
            ["sudo", "tar", "-C", "/usr/local", "-xzf", go_file], 
            capture_output=True, 
            text=True, 
            timeout=120
        )
        if result.returncode != 0:
            print(f"Failed to extract Go: {result.stderr}")
            return False
        
        # 3. Set environment variables
        print("Setting up Go environment variables...")
        go_path = 'export PATH=$PATH:/usr/local/go/bin'
        
        # Check if it already exists
        profile_path = os.path.expanduser("~/.profile")
        if os.path.exists(profile_path):
            with open(profile_path, 'r') as f:
                content = f.read()
                if go_path not in content:
                    with open(profile_path, 'a') as f:
                        f.write(f"\n{go_path}\n")
        else:
            with open(profile_path, 'w') as f:
                f.write(f"{go_path}\n")
        
        # 4. Update the PATH of the current session
        os.environ['PATH'] = os.environ['PATH'] + ':/usr/local/go/bin'
        
        # 5. Verify the installation
        time.sleep(2)  # Wait for the environment variables to take effect
        if check_go_installed():
            print("Go installed successfully")
            # Clean up the downloaded file
            if os.path.exists(go_file):
                os.remove(go_file)
            return True
        else:
            print("Go installation verification failed")
            return False
        
    except subprocess.TimeoutExpired:
        print("Go installation timed out")
        return False
    except Exception as e:
        print(f"Go installation failed: {e}")
        return False

def main():
    print("Linux Environment Setup Script")
    
    try:
        success = environment_setup()
        
        if success:
            print("Environment setup completed successfully")
            sys.exit(0)
        else:
            print("Environment setup failed")
            sys.exit(1)
            
    except KeyboardInterrupt:
        print("\nEnvironment setup interrupted by user")
        sys.exit(1)
    except Exception as e:
        print(f"Environment setup error: {e}")
        sys.exit(1)

if __name__ == "__main__":
    main()