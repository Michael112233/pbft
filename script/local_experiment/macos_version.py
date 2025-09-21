
import subprocess
import argparse
import sys
import time
import os


class MacOSLocalExperiment:
    def __init__(self, node_num=4):
        self.node_num = node_num
        self.processes = []

    # Set up environment
    def setup_environment(self):
        try:
            # Check if Go is installed
            result = subprocess.run([
                "go", "version"
            ], capture_output=True, text=True, timeout=10)
            
            if result.returncode != 0:
                print(f"Go is not installed or not working properly: {result.stderr}")
                return False
            
            print(f"Go is already installed: {result.stdout.strip()}")
            print("Environment setup completed successfully (macOS)")
            return True

        except subprocess.TimeoutExpired:
            print(f"Environment setup timed out")
            return False
        except Exception as e:
            print(f"Environment setup failed with error: {e}")
            return False

    # Build the Go project
    def build_go_project(self):
        try:
            # 1. run "go mod tidy"
            print("Running go mod tidy...")
            result = subprocess.run([
                "go", "mod", "tidy"
            ], capture_output=True, text=True, timeout=60)
            
            if result.returncode != 0:
                print(f"go mod tidy failed: {result.stderr}")
                return False
            
            # 2. build the executable file
            print("Building Go project...")
            result = subprocess.run([
                "go", "build", "-o", "pbft_main", "main.go"
            ], capture_output=True, text=True, timeout=120)
            
            if result.returncode != 0:
                print(f"Build failed: {result.stderr}")
                return False
            
            print(f"Build completed successfully")
            return True

        except subprocess.TimeoutExpired:
            print(f"Build timed out")
            return False
        except Exception as e:
            print(f"Build failed with error: {e}")
            return False

    # Start multiple terminals to run
    def start_terminals(self):
        try:
            # 0. Close all existing terminals first
            print("Closing existing terminals before starting new ones...")
            self.close_all_terminals()
            
            # 1. start node terminals
            for node_id in range(self.node_num):
                self.start_node_terminal(node_id)
                print(f"Node {node_id} terminal started")

            # 2. start client terminal
            self.start_client_terminal()
            print(f"Client terminal started")

            return True

        except Exception as e:
            print(f"Start terminals failed with error: {e}")
            return False


    # Implement Client Terminal Function
    def start_client_terminal(self):
        try:
            # Use AppleScript to Control Terminal.app
            applescript = f'''
            tell application "Terminal"
                activate
                do script "cd {os.getcwd()} && ./pbft_main -r client -m local"
            end tell
            '''
            
            subprocess.run([
                "osascript", "-e", applescript
            ], check=True)
            
            print("Client terminal started")
            
        except subprocess.CalledProcessError as e:
            print(f"Failed to start client terminal: {e}")
            raise

    # Implement Node Terminal Function
    def start_node_terminal(self, node_id):
        try:
            applescript = f'''
            tell application "Terminal"
                activate
                do script "cd {os.getcwd()} && ./pbft_main -r node -m local -n {node_id}"
            end tell
            '''
            
            subprocess.run([
                "osascript", "-e", applescript
            ], check=True)
            
            print(f"Node {node_id} terminal started")
            
        except subprocess.CalledProcessError as e:
            print(f"Failed to start node {node_id} terminal: {e}")
            raise


    # Run the Whole Experiment
    def run_experiment(self):
        print("Starting macOS Local PBFT Experiment")
        try:
            # Step 0: Cleanup before starting
            print("Performing pre-experiment cleanup...")
            self.clean_log_files()
            
            # Step 1: Setup Environment
            if not self.setup_environment():
                print("Environment setup failed")
                return False
            
            # Step 2: Build and Organize Project
            if not self.build_go_project():
                print("Project build failed")
                return False
            
            # Step 3: Start Terminals
            if not self.start_terminals():
                print("Terminal startup failed")
                return False

            print("Experiment started successfully!")
            print("Press Ctrl+C to stop the experiment")
            
            # Wait for user interrupt
            self.wait_for_interrupt()
            
            return True
        
        except KeyboardInterrupt:
            print("\nExperiment interrupted by user")
            return False
        except Exception as e:
            print(f"Experiment error: {e}")
            return False

    def wait_for_interrupt(self):
        try:
            while True:
                time.sleep(1)
        except KeyboardInterrupt:
            print("\nShutting down experiment...")
            self.cleanup()

    def cleanup(self):
        print("Cleaning up...")
        
        # Close all terminals
        try:
            applescript = '''
            tell application "Terminal"
                close every window
            end tell
            '''
            subprocess.run(["osascript", "-e", applescript], check=True)
            print("All terminals closed")
        except:
            print("Failed to close terminals")

    def clean_ports(self):
        # Clean ports
        required_ports = [20000, 28000, 28100, 28200, 28300]
        
        for port in required_ports:
            try:
                # Use lsof to find the processes occupying the ports
                result = subprocess.run([
                    "lsof", "-ti", f":{port}"
                ], capture_output=True, text=True)
                
                if result.returncode == 0 and result.stdout.strip():
                    pids = result.stdout.strip().split('\n')
                    for pid in pids:
                        subprocess.run(["kill", "-9", pid], check=True)
                        print(f"Killed process {pid} on port {port}")
            except:
                pass

    def close_all_terminals(self):
        """Close all unused terminals before starting the experiment"""
        try:
            print("Closing all unused terminals...")
            applescript = '''
            tell application "Terminal"
                close every window
            end tell
            '''
            subprocess.run(["osascript", "-e", applescript], check=True)
            print("All terminals closed successfully")
            return True
        except subprocess.CalledProcessError as e:
            print(f"Failed to close terminals: {e}")
            return False
        except Exception as e:
            print(f"Error closing terminals: {e}")
            return False

    def clean_log_files(self):
        """Delete all log files before starting the experiment"""
        try:
            print("Cleaning log files...")
            logs_dir = os.path.join(os.getcwd(), "logs")
            
            if not os.path.exists(logs_dir):
                print("Logs directory does not exist")
                return True
            
            # List of log files to clean
            log_files = [
                "blockchain.log",
                "client.log", 
                "node_0.log",
                "node_1.log",
                "node_2.log",
                "node_3.log",
                "others.log",
                "result.log"
            ]
            
            cleaned_count = 0
            for log_file in log_files:
                log_path = os.path.join(logs_dir, log_file)
                if os.path.exists(log_path):
                    os.remove(log_path)
                    cleaned_count += 1
                    print(f"Deleted {log_file}")
            
            print(f"Cleaned {cleaned_count} log files")
            return True
            
        except Exception as e:
            print(f"Error cleaning log files: {e}")
            return False
    

def main():
    parser = argparse.ArgumentParser(description='macOS Local PBFT Experiment')
    parser.add_argument('node_count', type=int, nargs='?', default=4,
                       help='Number of nodes to start (default: 4)')
    
    args = parser.parse_args()
    
    if args.node_count < 1 or args.node_count > 10:
        print("Error: Node count must be between 1 and 10")
        sys.exit(1)
    
    experiment = MacOSLocalExperiment(args.node_count)
    success = experiment.run_experiment()
    
    if success:
        print("Experiment completed successfully")
    else:
        print("Experiment failed")
        sys.exit(1)

if __name__ == "__main__":
    main()