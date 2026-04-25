# 嵌入邮件 HTML（go:embed）

发信侧请使用 `mailstatic.RenderHTML` 与 `html/*.html`，由 `//go:embed` 打进二进制，**不读数据库 MailTemplate**。

维护模版：直接编辑本目录下 `html/` 内文件（Go `html/template` 语法，如 `{{.Username}}`）。
