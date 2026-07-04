# StudyQuest (学途奇旅) 前端设计与优化规范 (design.md)

本文件详细记录了 StudyQuest 的项目定位、现有前端功能与实现、当前设计系统以及后续前端优化的核心意图。以便设计系统与前端人员（如 stitch）能够以此为基础进行高水准的 UI/UX 重构与细节设计。

---

## 1. 项目定位与核心意图 (Product Intent)

* **定位**：StudyQuest 是一款**长期个人学习资源管理平台**。其核心意图是家长自行搭建的私有学习资料统一入口，将散落在各种网盘（如 115网盘、天翼云、夸克等，通过 AList/WebDAV 挂载）中的课外视频资源，智能转化为有结构、有播放进度追踪、有 AI 探险卡和课后小挑战互动的自主学习体验。
* **用户角色与控制感**：
  * **学生端（Client）**：初期主要面向小学 3-6 年级儿童。设计上应当极其游戏化、多插图、高饱和度或精美暗色调、且尽量减少生硬的长篇文字交互。配合积分系统，将“背单词、看课外纪录片、学数学思维”包装为一场“探险通关之旅”。
  * **家长端（Admin Panel）**：属于管理和控制侧。家长在此导入网盘视频、分配课程访问权限、调整 AI 挑战题的生成等。
* **多端适配与遥控器交互 (D-Pad Optimization)**：
  * 客户端使用 Flutter 编写，同一套代码同时编译并运行在 **Android 平板 (PAD)** 与 **Android 电视 (TV/电视盒子)** 上。
  * **电视端必须原生支持物理遥控器/D-pad 焦点系统**。所有按钮和卡片都必须有非常明确且符合直觉的焦点移动逻辑，并拥有吸睛的焦点激活态反馈（如发光、放大或框线动画）。

---

## 2. 现有前端页面与核心功能 (Current Features)

目前系统拥有 **学习客户端 (Flutter Client)** 和 **管理后台 (Go Admin Panel)** 两个主要前端组成部分。

### 2.1 学习客户端 (Flutter Client) 页面功能

目前客户端的 Flutter 代码在 [frontend/lib](file:///home/revin/repos/study-quest/frontend/lib) 下，已经实现了以下界面和交互：

1. **多用户登录页 ([login_screen.dart](file:///home/revin/repos/study-quest/frontend/lib/ui/screen/login_screen.dart))**：
   * 启动后展示所有已被授权的学生头像与昵称。
   * 点击/选择学生后，弹出一个磨砂黑的 PIN 码输入板 ([num_pad.dart](file:///home/revin/repos/study-quest/frontend/lib/ui/widget/num_pad.dart))，学生需输入 4-6 位数 PIN 码。
   * PIN 码通过加密传输，后端使用 bcrypt 校验，校验成功则进入系统主导航。如果服务器配置失败，提供去配置 IP 的入口。
2. **主导航架构 ([main_navigation.dart](file:///home/revin/repos/study-quest/frontend/lib/ui/screen/main_navigation.dart))**：
   * 采用经典的游戏左侧导航栏（含有当前学生头像卡片、积分显示、三个功能标签页：“学习大厅”、“我的足迹”、“设置中心”，以及退出切换账号按钮）。
   * 专为 D-pad 遥控器优化，支持从左侧边栏向右移动焦点进入内容区。
3. **学习大厅 ([course_list_screen.dart](file:///home/revin/repos/study-quest/frontend/lib/ui/screen/course_list_screen.dart))**：
   * 展示该学生已被分配授权的课程网格 (Grid View)。
   * 每张课程卡片包含：课程封面大图、课程标题、适用年级标签 (如 3年级)、所属科目 (如 语文 📚, 数学 📐, 英语 🔠, 科学 🧪, 百科 🌎)。
   * 响应式布局：根据屏幕宽度自动切分列数（大平板/TV 显示 3 列，小屏幕显示 2 列）。
4. **课程详情页 ([course_detail_screen.dart](file:///home/revin/repos/study-quest/frontend/lib/ui/screen/course_detail_screen.dart))**：
   * 左右分栏：
     * **左侧**：大型课程封面、年级与科目徽章、标题描述。
     * **右侧**：垂直线性课时时间轴 (Episode Timeline)。每一个课时节点有一个指示器，显示如 `P1`, `P2` 等序号，已通关的课时显示绿色小勾，未通关的显示紫色播放按钮。
5. **视频播放与 AI Blocker 互动页 ([player_screen.dart](file:///home/revin/repos/study-quest/frontend/lib/ui/screen/player_screen.dart))**：
   * 视频流由 Go 后端通过 API 动态解析网盘（如 AList/WebDAV），并返回带有鉴权 Header 的直链，由播放器直接拉流，不经过 Go 服务器中转。
   * **交互拦截器 1：课前探险卡 (Pre-adventure Explorer Cards)**：
     * 在视频初始化完成后，并不立即播放，而是暂停并弹出浮层。
     * 轮播展示 1~3 张“探险思考卡”（AI 预先从视频字幕中提取的引导思考题），学生必须点击“下一张”，阅读完毕后才能解锁视频播放。
   * **互动拦截器 2：课后小挑战 (Post-watch Quiz)**：
     * 视频播放过程中有防作弊监听，每 5 秒上报一次进度。
     * 当视频播放进度达到 **80%** 时，视频自动暂停并弹出答题浮层。
     * 显示由 AI 生成的选择题，包含问题描述与多个选项。
     * 学生选择选项后立即锁定，并提供视觉反馈（正确显示绿色边框，错误显示红色边框，并标识正确答案）。
     * 回答完毕所有问题后，显示通关喜报：展示通关关卡名称、获得的积分奖励（答对 1 题得 10 分）。
     * 点击“关闭”后，提交进度并更新通关状态，安全退出播放器返回详情页。
6. **成长足迹页 (My Progress in [main_navigation.dart](file:///home/revin/repos/study-quest/frontend/lib/ui/screen/main_navigation.dart#L187-L326))**：
   * 统计模块：
     * 积分卡片：展示当前可用积分、累计获得积分，带星星图标。
     * 通关卡片：展示已通关课时数、已参与学习的课时总数，带对勾图标。
   * 明细列表：以卡片形式展示近期学习通关的课时历史记录与时间。
7. **客户端设置页 (Settings in [main_navigation.dart](file:///home/revin/repos/study-quest/frontend/lib/ui/screen/main_navigation.dart#L329-L390))**：
   * 提供文本输入框，允许输入或恢复默认的局域网后端 API 地址 (例如 `http://192.168.1.100:8080`)。

### 2.2 管理后台 (Go Admin Panel) 页面功能

管理后台目前集成在 Go 后端中，使用 HTML 模板渲染，代码在 [backend/internal/admin/templates](file:///home/revin/repos/study-quest/backend/internal/admin/templates) 下，主要功能包括：

1. **登录页 (`login.html`)**：管理员独立密码登录，密码经过 Bcrypt 处理并存储在 `settings` 系统配置表中。
2. **仪表盘 (`dashboard.html`)**：展示系统基础状况与快速导航。
3. **用户管理 (`users.html`)**：创建/编辑学生账号、修改 PIN 码、上传/配置头像 URL、定义角色。
4. **课程管理 (`courses.html`)**：创建、编辑和删除课程包，编辑年级、科目，并可关联已扫描导入的课时视频。
5. **网盘导入向导 (`import.html`)**：
   * 管理员选择网盘（AList/WebDAV）物理路径，系统递归扫描目录。
   * **智能映射识别**：将根目录自动识别为 Course，子目录识别为 Chapter（章节），视频文件识别为 Episode（课时）。
   * **目录穿透**：支持管理员手动勾选跳过某些空心、层级过深的文件夹（Skip），其下属的视频会自动“浮动”挂载到最近的有效上级节点中。
   * **预览并导入**：在页面上直接微调课程/课时名称，剔除无用文件，一键导入数据库。
6. **系统设置 (`settings.html`)**：
   * 配置网盘存储源连接（AList URL / Token 或 WebDAV 地址与账号密码）。
   * 配置 DeepSeek 等大语言模型 API Key 与端点，供 AI 后台服务生成探险卡和选择题。

---

## 3. 当前 UI 设计系统规范 (Theme Tokens)

现有的 Flutter 客户端定义了以下规范以支持大圆角、游戏化的 Switch 风格：

* **核心色板** (定义在 [theme.dart](file:///home/revin/repos/study-quest/frontend/lib/theme.dart))：
  * `backgroundColor = Color(0xFF0B0F19)`：暗黑深邃的星空底色。
  * `cardColor = Color(0xFF111827)`：深蓝灰色，用于内容卡片和容器背景。
  * `primaryColor = Color(0xFF8B5CF6)`：主题紫色，用于主要按钮、选中的导航、激活的焦点边框。
  * `accentGreen = Color(0xFF10B981)`：薄荷绿，代表完成、通过、正确答案和增加的积分。
  * `accentOrange = Color(0xFFF59E0B)`：温暖橙，用于积分星星、荣誉和提醒。
  * `textWhite = Color(0xFFF1F5F9)`：主要文字颜色。
  * `textMuted = Color(0xFF9CA3AF)`：次要/辅助说明文字。
  * `borderMuted = Color(0xFF1F2937)`：暗灰蓝色，用于默认边框线。
* **高辨识度的边框与圆角**：
  * 卡片和按钮的圆角全部统一采用高圆角：`BorderRadius.circular(18.0)`。
  * 默认边框厚度为：`3.0`。
* **遥控器焦点感知设计 (D-pad Focus Decoration)**：
  * 使用自定义包装组件 `FocusButton`。当小组件获得遥控器焦点时：
    * 边框由 `borderMuted` 渐变为 `primaryColor` (幻彩紫)。
    * 附加明显的 `boxShadow` 外发光呼吸效果：`primaryColor.withOpacity(0.4)`, 模糊半径为 `12`。

---

## 4. 前端优化意图与 Stitch 设计指导 (Optimization Goals)

目前的前端实现已经打通了业务功能，但是在**视觉档次、游戏化交互趣味、流畅微动效、数据大屏质感**上面还非常欠缺。我们希望进行如下维度的彻底升级与重构：

### 4.1 引入更高级的视觉质感 (Premium Aesthetics)

1. **渐变与磨砂玻璃 (Glassmorphism & Gradients)**
   * 底色不能只是一片死板的黑色。引入微妙的背景动态渐变，比如极弱的星空粒子或流光溢彩（Auroral Glow）。
   * 弹出层（如 PIN 码输入板、探险卡、答题小挑战）改用高档的**毛玻璃 (Frosted Glass)** 效果，使整体界面的视觉深度更强。
2. **高质量插图与游戏化图标**
   * 取代目前生硬的系统原生 Icon，设计一套成体系的卡通/探险手绘风格图标。
   * 为不同科目 (语文、数学、英语、百科) 绘制专属的游戏化科目插画封面或卡片背景。

### 4.2 重构游戏化闯关地图路线 (Interactive Path Map)

* **摆脱普通列表**：将课程详情页中右侧呆板的垂直列表重构为类似 **多邻国 (Duolingo) 的蜿蜒探险地图路线 (Path Map)**。
* **路线设计**：
  * 课时 Episode 作为一个个沿着小径分布的关卡节点（可以是小星球、宝箱或石碑）。
  * 路线周围可以摆放与该课程主题相关的趣味插画元素。
  * 学生通关 P1 之后，路线会平滑亮起，延伸到 P2。
  * 点击某个关卡节点时，在节点上方弹出精美气泡（Popover），展示课时名和开始学习按钮。

### 4.3 精雕细琢的微动效与欢庆体验 (Micro-interactions & Celebration)

1. **焦点的动态过渡 (Focus Transitions)**
   * D-pad 移动焦点时，卡片选中状态的切换应有极其丝滑的**平移动画**，或者缩放微动效 (Scale Transition)，而不是生硬地直接跳转。
2. **通关与答题的爽快感**
   * **探险卡翻牌**：课前探险卡设计成实体卡牌的 3D 翻转动效 (Card Flip Animation)。
   * **答题交互**：答对选项时，选项卡片向上弹跳，并向周围喷洒出彩带粒子；答错时卡片左右晃动 (Shake Animation) 提示错误。
   * **金币飞入**：答题完全通过后，奖励的积分数值应以滚数字 (Number Counter) 的形式跳动，金币/星星伴随声效从屏幕中央飞入左上角或右上角的学生积分统计池。

### 4.4 管理后台 (Admin Web) 彻底升级为现代化 SPA 看板

* **高颜值现代化 Dashboard**：
  * 后台应当脱离古板的 HTML 表单。采用以 Tailwind CSS / Vanilla CSS 深度优化的现代控制台布局。
  * **数据可视化**：在首页仪表盘中，绘制精美的学习时长趋势图（折线图/柱状图），统计各科目的积分比重（环形图/雷达图）。
* **智能导入树的重构**：
  * 重新设计云盘扫描与导入界面。左侧显示直观的文件树，支持折叠、一键勾选/反选；右侧同步显示映射后的“逻辑章节-课时”预览。
  * 用不同颜色的徽章（如 [新导入]、[路径已变更/认亲中]、[已存在]、[已穿透]）标示各物理文件的状态，支持快捷双击行内编辑逻辑名称，提升家长的运维便利性。

---

## 5. 建议的技术实现路径参考

1. **客户端 (Flutter)**：
   * 采用 `Flutter Animate` 或 `Lottie` 导入精美轻量动画，提升欢庆界面的趣味性。
   * 使用 `CustomPainter` 或 `flutter_map_path` 构建多邻国关卡地图组件。
   * 保持现有的 `FocusNode` 与 `FocusScope` 机制，在此之上用 `AnimatedContainer` 承接焦点事件，实现有过渡动画的焦点发光环。
2. **后台管理端 (Admin)**：
   * 探索基于 SPA 框架（如 React / Vite / Vue）构建高颜值看板。
   * 打包后的静态资源通过 Go 的 `go:embed` 统一内嵌编译至 Go 后端可执行文件中，继续保持“单文件、零依赖”的极简服务器运维体验。
