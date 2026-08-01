# StudyQuest 前端设计

> 本文件是**前端的产品定位 + 功能盘点**。视觉 token(色值/字号/圆角/焦点态)的
> 单一真相源是 [`docs/design-tokens.md`](../../design-tokens.md),这里不重复写色值,
> 只讲三端分工与各端页面做什么。

## 1. 产品定位

家长自建的**私有学习资源管理平台**:把网盘(115/天翼云/夸克,经 AList/WebDAV 挂载)
里散落的课外视频,变成有结构、有进度追踪、有 AI 互动的自主学习体验。

- **学生端**:面向小学中高年级。游戏化、多插图、高饱和度或精美暗色(TV),尽量减少
  长篇文字。配合积分系统,把"看课外纪录片、学数学思维"包装成"探险通关之旅"。
- **家长端(Admin)**:管理与控制侧。导入网盘视频、配课程/章节、配 AI、授权孩子访问。

## 2. 三端分工

| 端 | 工程 | 技术 | 角色 |
|---|---|---|---|
| 学生端 PAD/手机 | `frontend/`(Flutter) | Flutter / Dart | 看课 + 闯关 + AI 学习 |
| TV 端 | `tv-android/` | Kotlin + Compose for TV | 大屏看课(独立工程,视觉 token 与 PAD 同源,**底色取向不同**:PAD 浅色主题、TV 深色主题) |
| 家长管理端 | `frontend-admin/`(React SPA) | React 18 + TS + Vite + Tailwind | 导入/编排/配 AI/看数据。构建产物 `go:embed` 内嵌进 Go 二进制,访问 `/admin` |

> Admin 已是 React SPA(不再是历史曾有的 Go HTML 模板后台)。详见
> [`frontend-admin/README.md`](../../../frontend-admin/README.md)。

## 3. 学生端(Flutter)页面功能

- **登录**:授权学生头像 + 4-6 位 PIN(bcrypt 校验),磨砂黑数字键盘。
- **主导航**:游戏化左侧栏(学生头像 + 积分 + 学习大厅 / 我的足迹 / 设置)。
- **学习大厅**:已授权课程网格(封面 + 标题 + 年级 + 科目徽章),响应式列数。
- **课程详情**:左右分栏(左侧课程封面/元数据,右侧课时时间轴,已通关显示绿色勾)。
- **视频播放**:网盘直链拉流(带鉴权 Header,不经 Go 中转);课前探险思考卡拦截;
  每 5s 上报观看进度;课后小挑战(quiz)入口。
- **AI 学习**(`ai_study_screen`):做 quiz / 看学习建议 / 复盘;统一交卷(submit-all)
  后归档为历史。三态门禁(ready/generating/unavailable)。
- **成长足迹**:积分 + 通关统计 + 近期学习明细。

## 4. TV 端(Kotlin / Compose for TV)

大屏看课为主,复用后端 `/api/v1/*`。物理遥控器 D-pad 焦点导航是硬要求(所有可点
元素都要有明确的焦点移动逻辑 + 吸睛焦点激活态)。视觉 token 见
[`docs/design-tokens.md`](../../design-tokens.md) 的 TV 段。

## 5. 家长管理端(React SPA)页面功能

- **Dashboard**:StatCard 网格 + 科目分布 + 近 7 天新增课时。
- **内容管理**:课程/章节/课时树、ffprobe 媒体元数据徽章、批量操作、字幕抽屉。
- **网盘导入**:三步向导(选路径 → 配导入目标 → 预览确认),智能映射
  (根目录→Course,子目录→Chapter,视频→Episode)+ 目录穿透。
- **AI 控制台**:对象即导航的课程/学生工作台(总结/作业/quiz 生成与重生成、prompt
  配置、术语候选审阅、润色、学习数据观测)。
- **用户/阅读室/设置**:学生账号 CRUD、阅读资源、存储与 AI provider 配置。
