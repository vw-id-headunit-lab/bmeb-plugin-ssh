# BetterMEB SSH 插件

BetterMEB 的独立 SSH 服务插件，使用车机已有的 OpenSSH，在 IPv4 TCP `2223` 上运行独立实例。

仓库名为 `bmeb-plugin-ssh`，BetterMEB 插件 ID 保持为 `sshd`，版本为 `0.2.0`。

## 安装

需要已安装 [BetterMEB](https://github.com/vw-id-headunit-lab/bettermeb)，以及 Linux `/proc`、`/bin/sh`、`sshd`、`ssh-keygen`、`iptables` 和脚本所用的常见 shell 工具。以 root 运行，依赖系统已准备好的 OpenSSH 运行环境。

将本仓库的 `manifest.json`、`run.sh`、`cleanup.sh` 放到车机：

```text
/lge/app_rw/bettermeb/plugins/sshd/
├── manifest.json
├── run.sh
└── cleanup.sh
```

给两个脚本执行权限：

```sh
chmod 755 /lge/app_rw/bettermeb/plugins/sshd/run.sh \
  /lge/app_rw/bettermeb/plugins/sshd/cleanup.sh
```

由 BetterMEB 加载插件。manifest 默认启用并自启动；BetterMEB 进入正常模式且 HMI 稳定后启动插件。插件没有用户配置页面，端口固定为 `2223`。

连接方式：

```sh
ssh -p 2223 root@<设备IPv4地址>
```

## 认证与持久数据

- 首次启动在 `PLUGIN_DATA_DIR/ssh_host_ed25519_key` 生成主机私钥，后续启动复用。
- 公钥授权文件为 `PLUGIN_DATA_DIR/authorized_keys`，首次创建为空。通过已有管理通道加入自己的客户端公钥，权限保持 `600`。
- 当前参数允许 root、公钥、密码和空密码认证（`AuthenticationMethods=any`）。**若设备 root 账号允许空密码，网络可达的客户端可能无需密码登录。**请按实际设备账号状态和网络环境部署。
- 插件不修改 root 账号或密码，不导入系统 SSH 的主机密钥或授权文件。
- 仓库不包含任何设备密钥、授权公钥或运行数据。

## 生命周期

运行入口为 `run.sh`，清理入口为 `cleanup.sh`。BetterMEB 注入：

| 环境变量 | 用途 |
| --- | --- |
| `PLUGIN_DIR` | 插件脚本所在目录 |
| `PLUGIN_DATA_DIR` | 持久化主机密钥和授权文件 |
| `PLUGIN_TMP_DIR` | 临时 `ready` 与 `cleanup.status` 回执 |
| `PLUGIN_GENERATION` | 写入就绪回执的启动代次；未提供时为 `0` |

插件校验 sshd 参数，幂等添加带 `bettermeb-sshd-2223` 注释的 IPv4 `INPUT` 放行规则，然后启动 sshd。确认本次子进程实际持有 2223 监听套接字后才报告就绪。端口冲突会启动失败，不接管其他监听实例。

停止时结束自己的子进程并移除自己的防火墙规则，保留持久数据。异常退出最多自动重试两次，退避分别为 2 秒、5 秒。系统原有 SSH 服务不由本插件管理。

## 已知日志提示

本次独立提取保留上游插件行为：

- `Unsupported option UsePAM`：脚本显式传入 `UsePAM=no`，未编译 PAM 支持的 OpenSSH 会输出此提示。通常不阻止启动；启动校验、正式启动及连接时重新解析参数可能重复输出。
- `iptables: Bad rule (does a matching rule exist in that chain?)`：启动和清理通过 `iptables -C` 查询规则，规则不存在时会有 stderr 输出；脚本将返回码 `1` 作为正常的“不存在”分支处理。清理成功删除规则后再检查，也可能出现一次。
- 新 SSH 连接本身不会调用本插件的防火墙脚本。若界面重复显示日志，应同时核对插件 PID、generation 与实时日志连接是否回放了缓存。

## 验证

Linux 上安装 Go 1.22 或更高版本及 `ssh-keygen` 后运行：

```sh
sh -n run.sh cleanup.sh
go test -v ./...
```

测试使用假的 sshd/iptables 子进程验证启动、就绪、停止清理、密钥复用、授权文件保留、端口冲突及防火墙查询异常；不更改宿主机防火墙或 SSH 服务。测试会临时占用本机回环地址的 2223 端口；端口已被占用时跳过生命周期测试。

## 来源

提取自 [BetterMEB](https://github.com/vw-id-headunit-lab/bettermeb) 提交 `aba6659fc425572d26e701426b30ac3de6928a19` 的 `deploy/plugins/sshd/`；生命周期测试来自 `deploy/sshd_test.go`，仅调整测试包名和插件目录路径。
