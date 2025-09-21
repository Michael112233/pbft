
import subprocess
import argparse
import sys
import time
import os


class LinuxLocalExperiment:
    def __init__(self, node_num=4):
        self.node_num = node_num
        self.processes = []

    # Set up environment
    def setup_environment(self):
        try:
            # use subprocess to run the environment_setup.sh
            result = subprocess.run([
                "python3", "./script/environment_setup.py"
            ], capture_output=True, text=True, timeout=60)
            
            if result.returncode != 0:
                print(f"Environment setup failed with error: {result.stderr}")
                return False
            print(f"Environment setup completed successfully")
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

    # Start multiple processes to run
    def start_terminals(self):
        try:
            # 0. Clean up any existing processes first
            print("Cleaning up existing processes...")
            self.cleanup_processes()
            
            # 1. start node processes
            for node_id in range(self.node_num):
                self.start_node_terminal(node_id)
                time.sleep(1)  # Small delay between starting nodes

            # 2. start client process
            self.start_client_terminal()

            return True

        except Exception as e:
            print(f"Start processes failed with error: {e}")
            return False


    # Implement Client Process Function
    def start_client_terminal(self):
        try:
            # Start client process in background
            process = subprocess.Popen([
                "./pbft_main", "-r", "client", "-m", "local"
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            
            self.processes.append(process)
            print(f"Client process started with PID {process.pid}")
            
        except Exception as e:
            print(f"Failed to start client process: {e}")
            raise

    # Implement Node Process Function
    def start_node_terminal(self, node_id):
        try:
            # Start node process in background
            process = subprocess.Popen([
                "./pbft_main", "-r", "node", "-m", "local", "-n", str(node_id)
            ], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
            
            self.processes.append(process)
            print(f"Node {node_id} process started with PID {process.pid}")
            
        except Exception as e:
            print(f"Failed to start node {node_id} process: {e}")
            raise


    # Run the Whole Experiment
    def run_experiment(self):
        print("Starting Linux Local PBFT Experiment")
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
                # Check if any process has died
                dead_processes = []
                for i, process in enumerate(self.processes):
                    if process.poll() is not None:
                        dead_processes.append(i)
                        print(f"Process {process.pid} has terminated")
                
                # Remove dead processes from the list
                for i in reversed(dead_processes):
                    self.processes.pop(i)
                
                # If all processes are dead, exit
                if not self.processes:
                    print("All processes have terminated")
                    break
                
                time.sleep(1)
        except KeyboardInterrupt:
            print("\nShutting down experiment...")
            self.cleanup()

    def cleanup_processes(self):
        """Clean up existing PBFT processes"""
        try:
            # Kill any existing pbft_main processes
            subprocess.run(["pkill", "-f", "pbft_main"], capture_output=True)
            print("Cleaned up existing PBFT processes")
        except:
            pass
    
    def cleanup(self):
        print("Cleaning up...")
        
        # Terminate all started processes
        for process in self.processes:
            try:
                if process.poll() is None:  # Process is still running
                    process.terminate()
                    try:
                        process.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        process.kill()
                    print(f"Terminated process {process.pid}")
            except:
                pass
        
        self.processes.clear()
        print("All processes cleaned up")

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
    
    experiment = LinuxLocalExperiment(args.node_count)
    success = experiment.run_experiment()
    
    if success:
        print("Experiment completed successfully")
    else:
        print("Experiment failed")
        sys.exit(1)

if __name__ == "__main__":
    main()