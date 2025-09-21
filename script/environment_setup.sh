sudo apt-get update
sudo apt-get install -y python3 python3-pip
pip install requests

wget https://go.dev/dl/go1.23.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.profile
source ~/.profile

go mod tidy
go build -o pbft_main main.go