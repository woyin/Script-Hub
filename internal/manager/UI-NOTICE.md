# 管理界面来源与许可

管理界面的导航、响应式容器、卡片、基础日间/夜间配色及编辑表单样式改编自 [Sub-Store Front-End](https://github.com/sub-store-org/Sub-Store-Front-End)，遵循 GNU GPL v3.0。仓库根目录的 `LICENSE` 提供许可全文。

参考版本：`8dc19320894a9dbb3569e8bf3e41057491e39b90`（2026-10-19 获取）。

改编来源：

- `src/themes/light.ts`、`dark.ts`：主题色，保留原作者 DesnLee 的设计。
- `src/assets/styles/mixins.scss`：600/768/900/1200 px 容器断点。
- `src/components/NavBar.vue`、`SideBar.vue`、`TabBar.vue`：导航与布局。
- `src/components/SubListItem.vue`、`FileListItem.vue`：资源卡片。
- `src/views/SubEditor.vue`、`FileEditor.vue`：编辑表单布局。
- `src/assets/icons/logo-red.png`、`file.svg`、`clash_color.png`、`shadowrocket_color.png`、`loon_color.png`、`egern_color.png`：原版资源及客户端图标，原样内嵌到 `icons.svg`。

本项目将这些样式和素材适配到原生 Go 管理 API；交互代码为本项目实现，不依赖原版 Node 后端。仅显示当前后端已支持的订阅、文件、分享和本地外观设置。没有原版同步、归档和 JavaScript 处理器功能。

改编文件：`index.html`、`management.css`、`icons.svg`。`management.js` 是适配本项目 API 的交互层。全部静态资源由 Go 内嵌，浏览器不向第三方加载资源。

工具栏中的实心图标来自 [Font Awesome Free 6.7.2](https://github.com/FortAwesome/Font-Awesome/tree/6.7.2/svgs/solid)，与原版使用的图标一致。Copyright Fonticons, Inc. 图标遵循 [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/)，路径原样嵌入 `icons.svg` 并以 `currentColor` 着色。其余线形图标与空状态插图为本项目绘制。
