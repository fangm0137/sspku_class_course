# Swagger UI（随镜像分发）

这里的静态文件来自 npm 包 `swagger-ui-dist` 5.33.2（Apache-2.0，见 `LICENSE`），
只取渲染文档所需的最小集合：

| 文件 | 用途 |
|---|---|
| `index.html` | 本项目自己写的挂载页：指向 `/openapi.yaml`，并复用浏览器里已保存的 `campusclaw_token` |
| `swagger-ui-bundle.js` | 上游产物，未作修改 |
| `swagger-ui.css` | 上游产物，未作修改 |
| `favicon-16x16.png` / `favicon-32x32.png` | 上游产物，未作修改 |
| `LICENSE` | 上游许可证（Apache-2.0） |

**为什么随仓库分发**：`docker compose up` 之后 `/swagger` 必须开箱可用，不能依赖
构建期或运行期的外网访问。这两个文件合计约 1.8 MB，是一次性产物，升级时按下面的
步骤重新取。

**升级办法**

```bash
curl -sL "https://registry.npmmirror.com/swagger-ui-dist/-/swagger-ui-dist-<版本>.tgz" -o swui.tgz
tar -xzf swui.tgz
cp package/swagger-ui-bundle.js package/swagger-ui.css package/LICENSE \
   package/favicon-16x16.png package/favicon-32x32.png deploy/swagger/
```

`index.html` 不用跟着换：它只依赖 `SwaggerUIBundle` 这个全局变量。
