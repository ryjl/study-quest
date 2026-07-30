# 部署

> 生产/公网部署的安全要点。开发环境见 `docs/dev-setup.md`。

## 现状(`make deploy`)

当前一键部署走 `Makefile` 的 `deploy` 目标:

1. 本地 `docker build` 出 `studyquest-backend:latest`(多阶段:ONNX 模型 + admin SPA + Go 二进制)。
2. `docker save | gzip | ssh` 把镜像推到远程主机(`ry@192.168.8.4:30901`)。
3. 远端 `docker run -d -p 6001:8080` 启动,**明文 HTTP**,无反向代理、无 TLS。

即:后端 8080 端口通过主机 6001 端口**直接暴露,明文传输**。这在局域网/家庭内网可用,
**公网部署前必须完成下面的加固**。

---

## 公网部署必做清单

### 1. HTTPS(反代终结 TLS)——PIN 明文传输的唯一解

学生端登录在 HTTP body 里明文传 PIN(`POST /api/v1/users/login` 的 `pin` 字段)。
明文 HTTP 下 PIN 可被中间人嗅探。**必须**用反向代理终结 TLS:

- **推荐 Caddy**:自动申请/续期 Let's Encrypt 证书,配置最简:
  ```caddyfile
  studyquest.example.com {
      reverse_proxy localhost:6001
  }
  ```
- **nginx + certbot** 同样可行,需手动配证书续期。

app **内部不做 TLS**(避免在反代后因 `X-Forwarded-Proto` 处理不当误伤)。证书与 TLS
完全由反代负责。确保反代正确转发协议头(`proxy_set_header X-Forwarded-Proto https`),
后端 `c.ClientIP()` 才能拿到真实客户端 IP(IP 限流才有效)。

### 2. ⚠️ admin 后台默认密码 "admin" —— 公网部署必须立即改

admin 后台是**独立登录体系**(不走 User 表 PIN),首启动若 settings 无 hash,会从默认值
`"admin"` 生成 bcrypt(`admin_auth.go`)。**公网部署后第一件事**:登录 admin 后台修改密码。

此外 admin 登录路由 `/admin/api/login` **当前没有任何限流**(裸挂,见 `router.go`),
不像学生端有 IP 限流 + 账户锁定。建议:
- 部署后**立即**把密码改成强密码;
- 在反代层对 `/admin/api/login` 额外加请求限流(如 nginx `limit_req`),降低在线爆破风险。

### 3. 爆破防护(代码已内置,部署需确认生效)

学生端 PIN 登录有**双层**防护(详见 `docs/architecture.md` §9):
- 按 IP 限流(同 IP 15min/5 次);
- 按 user_id 账户锁定(同账户 15min/5 次失败锁)。

两者都依赖 `c.ClientIP()` 拿真实客户端 IP —— **反代必须正确转发 IP 头**,否则所有请求
看起来都来自 127.0.0.1,IP 限流失效(账户锁定仍有效,因为是按 user_id)。

### 4. 数据库不暴露公网

SQLite 数据文件(`/app/data`)含 bcrypt PIN hash、admin 密码 hash、会话 token。bcrypt
缓解离线破解,但根本防线是**数据库文件不暴露公网**:只开放反代的 443,不开 6001/8080,
不开 SSH 给公网(用密钥 + 限制来源)。PIN 熵仅 ~20 bit(6 位数字),DB 泄露后离线破解
仍快,所以 DB 隔离是第一道、也是最关键的防线。

### 账户锁定的已知权衡

按 user_id 锁定是堵"换 IP 池针对单账户爆破"的必要手段,但有一个固有副作用:
攻击者知道某 user_id 后,可用错 PIN 反复触发锁定,把该正常用户**临时锁死 15 分钟**
(账户级 DoS)。这是所有"按账户锁定"方案的共有权衡。在家庭/小规模场景下可接受
(锁定有窗口、自动恢复、IP 限流前置挡住自动化);若未来对此敏感,可叠加验证码或
提高锁定阈值。注意:不存在的 user_id 不会产生锁定记录(Authenticate 在 user==nil 时
于 recordFailure 之前返回),所以探测式锁定只对真实账户有效。

---

## 单实例 vs 多副本

当前是**单实例**(一台 VPS 一个容器)。上面的限流/锁定都是**进程内内存状态**:
- 进程重启会清空所有计数器(IP 限流、账户锁定都会重置)。
- 这对家庭/单机部署可接受;若未来横向扩展多副本,需改用分布式存储(Redis 等)共享计数,
  否则每个副本各自计数,阈值实际放大 N 倍。
