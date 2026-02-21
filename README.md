# Mix Space Core

这是以 Golang 重写的 Mix Space Core

> [!WARNING]
> [我](https://github.com/BLxcwg666) 并不建议你使用此项目进行 Mix Space 部署  
> 此项目基于 `mx-space/core` 9.7.0 做了 API 兼容，独立于主线，因此不会继续跟进主线的更改  
> 您可以对此项目进行贡献，但我仍希望你去支持本家 [Innei](https://github.com/Innei) 的作品

本家: https://github.com/mx-space/core

## 环境要求

- **Go**: >= 1.22
- **Redis**: >= 7.4.2
- **MiliSearch**: >= 1.35.1

## 开发

我懒得写 go test file，所以没有 test 环节  
- 启动：`go run ./cmd/server`
- 指定配置文件：`go run ./cmd/server --config ./config.yml`（若未指定则使用二进制同级目录的`config.yml`）

# 许可

GNU Affero General Public License v3.0 (AGPLv3)  
项目灵感 / 思路 / 知识产权仍属于 [Innei](https://github.com/Innei)