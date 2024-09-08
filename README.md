# netMonitor
一款基于Golang的Linux流量统计与提醒工具，支持telegram消息和自动关机。





```
CGO_ENABLED=0 go build -trimpath -ldflags="-w -s" -o netmonitor ../src/main.go 
```
```
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-w -s" -o netmonitor-linux-amd64 ../src/main.go 
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-w -s" -o netmonitor-linux-arm64 ../src/main.go 
```
