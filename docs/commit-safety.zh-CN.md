# 检查提交中的敏感内容

[English](commit-safety.md)

启用仓库的 Git hook，在提交前拦截敏感内容。检查需要 Git 和 Python 3.10 或更高版本，不依赖第三方包。

## 启用检查

1. 在每个克隆的仓库根目录运行：

   ```bash
   make hooks-install
   ```

2. 暂存修改，然后手动检查或正常提交：

   ```bash
   git add <paths>
   make sensitive-check
   git commit
   ```

安装器将本地 `core.hooksPath` 设置为 `.githooks`。如果已有 hook 配置或 hook 文件，安装器会拒绝覆盖。如果已经使用其他 hook，请将 `python3 tools/git/check_sensitive.py` 加入现有的 pre-commit hook，在仓库根目录运行，并在检查返回非零状态时终止提交。

## 修复被拦截的提交

检查读取暂存区中新增、修改、复制、重命名或类型变更文件的完整内容。未暂存的修改不影响检查结果，删除的文件不参与检查。报告只包含文件名、行号和规则，不显示匹配到的值。读取 Git 数据失败也会阻止提交。

1. 用 `${VPS_HOST}`、`${VPS_USER}` 或 `${WAVE_MODEL_API_KEY}` 等环境变量引用替换真实值，将实际值保存在已忽略的本地配置文件中。
2. 文档和测试使用 `host.example.com`、`192.0.2.1` 或 `2001:db8::1` 等保留示例值。
3. 重新暂存修正后的文件，再运行 `make sensitive-check`。

检查规则包括：

- 私钥头、常见服务商的令牌格式和 JWT 格式字符串。
- 引号内的凭据赋值、环境变量赋值和 Bearer 值，匹配由受支持字符组成且长度至少为 16 个字符的值。
- 可公网路由的 IPv4 和 IPv6 字面值，包括映射 IPv4 的 IPv6 地址。
- 部署连接变量中的固定值、SSH 命令目标和包含密码的 URL。
- `.env`、`.env.production`、`*.local`、私钥文件及常见凭据存储等敏感文件名。以 `.example` 结尾的文件免除文件名检查，但仍检查内容。

允许回环、私网、文档示例、组播和已废弃的 6to4 中继地址，允许示例域名和本地数据库测试 URL。检查器的 `TEST_CREDENTIALS` 明确列出四个现有的虚构测试凭据；测试文件的其他内容正常检查。二进制文件只检查文件名、私钥、服务商令牌和 JWT，不检查 IP 和通用凭据。

## 执行审计

```bash
make sensitive-audit              # 检查暂存区中的所有已跟踪文件。
make sensitive-test               # 运行检查器和 hook 的回归测试。
python3 tools/git/check_sensitive.py --range BASE..HEAD
```

范围模式逐个检查范围内各提交中变更的文件，包括后来被修复或删除的中间提交。**Sensitive content** GitHub Actions 工作流对 PR 和推送到 `main` 或 `master` 的提交使用此模式。若要阻止未通过检查的 PR 合并，请在分支保护中将 `sensitive-content` 任务设为必需状态检查。CI 在上传后运行；本地 hook 在提交前运行。

## 限制

检查依赖模式匹配，无法识别所有密钥、正文中的任意主机名、编码后的值或全部二进制内容。少于 16 个字符的通用凭据可能通过检查。提交前仍需审查暂存内容。本地 hook 可以被绕过，每个克隆都需要单独安装。工作流不会自动配置分支保护。

检查不会清理现有 Git 历史。如果真实凭据已经提交，请撤销或轮换凭据，并单独处理历史清理。
