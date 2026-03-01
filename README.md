# Mix Space Core

> [!WARNING]
> 没写完，别急！！！

这是以 Golang 重写的 Mix Space Core

> [!WARNING]
> [我](https://github.com/BLxcwg666) 并不建议你使用此项目进行 Mix Space 部署  
> 此项目基于 `mx-space/core` 9.7.0 做了 API 兼容，独立于主线，因此不会继续跟进主线的更改  
> 您可以对此项目进行贡献，但我仍希望你去支持本家 [Innei](https://github.com/Innei) 的作品

本家: https://github.com/mx-space/core

## 环境要求

- **Go**: >= 1.22
- **Redis**: >= 7.4.2
- **MySQL**: >= 8.0.0
- **MiliSearch**: >= 1.35.1

## 管理员面板
额暂时呢我不会把面板本体附带到 Go 编辑的二进制产物中  
所以需要自行编译面板并在 `config.yml` 中填写 `dist` 路径  
面板版本以 [BLxcwg666/mx-admin](https://github.com/BLxcwg666/mx-admin) 为准，其他版本及原作者的版本兼容性将不被保证

## 开发

我懒得写 go test file，所以没有 test 环节  
- 启动：`go run ./cmd/server`
- 指定配置文件：`go run ./cmd/server --config ./config.yml`（若未指定则使用二进制同级目录的`config.yml`）
- 集群模式：`go run ./cmd/server --cluster --cluster_workers 2`

# 许可

本项目采用 GNU Affero General Public License v3.0 (AGPLv3) 开源

# 致谢与声明
- API 设计及原始逻辑来源于 [Innei](https://github.com/Innei) 的 [mx-space/core](https://github.com/mx-space/core)。
- 本项目是该作品的 Golang 兼容实现
