sudo apt-get update
sudo apt-get install -y python3 python3-pip
pip install requests

# Download and install Go 1.23.0
wget https://go.dev/dl/go1.23.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz

# Set up Go environment variables
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
echo 'export GOPATH=$HOME/go' >> ~/.bashrc
echo 'export PATH=$PATH:$GOPATH/bin' >> ~/.bashrc

# Clean up downloaded archive
rm go1.23.0.linux-amd64.tar.gz

# Source the updated configuration
source ~/.bashrc

# Verify Go installation
/usr/local/go/bin/go version