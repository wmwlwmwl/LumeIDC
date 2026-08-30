# 前端第三方资源

LumeIDC 将以下发行版嵌入 Go 二进制，运行时不需要 npm、Node.js 或 CDN。版本升级时请同步更新资源目录与校验清单。

- Bootstrap 5.3.3：`internal/handler/assets/vendor/bootstrap-5.3.3/`，MIT License
- Bootstrap 4.6.2（仅上游模块隔离页）：`internal/handler/assets/vendor/legacy-module/bootstrap-4.6.2.min.css` 与 `bootstrap-4.6.2.bundle.min.js`，MIT License
- jQuery 3.6.4（仅上游模块隔离页）：`internal/handler/assets/vendor/legacy-module/jquery-3.6.4.min.js`，MIT License
- SweetAlert2 11（仅上游模块隔离页）：`internal/handler/assets/vendor/legacy-module/sweetalert2-11.all.min.js`，MIT License
- Apache ECharts 5.5.1：`internal/handler/assets/vendor/echarts-5.5.1/`，Apache License 2.0

资源来源：对应项目的 npm/jsDelivr 官方发行文件。文件 SHA-256 见仓库根目录 `assets-manifest.sha256`。完整许可证文本和版权声明应随发布包一并保留；本项目自身代码仍采用仓库根目录的 MIT License。
