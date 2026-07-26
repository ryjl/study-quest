// HomeworkPreviewModal.tsx — 作业卷预览弹窗(RegenTab 行内「查看作业」按钮打开)。
//
// v2 新建(2026-07-26)。和 standalone Homework.tsx(已删)的预览逻辑等价,但改为 Modal
// 形态:勾选式生成后,每行右侧「查看作业」按钮 → 打开本 Modal → 卷面预览 + 工具栏
// (显示答案 toggle + 打印按钮)。
//
// 打印策略(v2 修复:用户报告"3 页都是第一页"重复):
// 原方案用 @media print 的 visibility:hidden + .print-area{position:absolute},
// 但 .print-area 在 Modal 的 position:fixed overlay 深层,absolute 元素跨页时浏览器
// 在每页重复绘制 → 3 页都是第一页。修复:用 createPortal 把"打印专用副本"渲染到
// body 顶层(document.body 的直接子节点),它不在 Modal overlay 里,打印态正常分页。
// 屏上只显示 Modal 内的预览(.hw-screen-only),打印副本(.hw-print-portal)屏上 display:none、
// 打印态显示。window.print() 结束后副本仍在 DOM(下次打印复用),Modal 关闭时一并卸载。
import { useState } from 'react';
import { createPortal } from 'react-dom';
import { useQuery } from '@tanstack/react-query';
import { AlertTriangle, Eye, EyeOff, Printer } from 'lucide-react';
import { Modal, Spinner } from '../ui';
import { api } from '../../lib/api';
import { formatDate } from '../../lib/format';
import { HomeworkPrintView } from './HomeworkPrintView';

export function HomeworkPreviewModal({
  open,
  onClose,
  homeworkId,
  courseTitle,
  subjectLabel,
  episodeTitle,
}: {
  open: boolean;
  onClose: () => void;
  homeworkId: number | null;
  courseTitle?: string;
  subjectLabel?: string;
  episodeTitle?: string;
}) {
  const [showAnswers, setShowAnswers] = useState(false);

  const viewQ = useQuery({
    queryKey: ['homework', homeworkId],
    queryFn: () => api.homeworkGet(homeworkId as number),
    enabled: open && homeworkId != null,
  });
  const view = viewQ.data;

  // handlePrint:v2 review 修两个 bug。
  //  (1) 给 body 加 .hw-printing 类,@media print 的卷面规则才生效(否则全局 #root
  //      display:none 会污染全站 Ctrl+P——进过 AI Console 后任何页打印都空白)。
  //      window.print() 同步阻塞返回后,或 afterprint 事件触发时,移除类。
  //  (2) 打印用独立的"无答案"状态:屏上 toggle showAnswers 只影响屏上预览,打印
  //      始终输出学生卷(无答案),避免老师批改时屏上开了答案、打印出来泄给学生。
  //      如果要打印答案版,用浏览器的"另存 PDF"在屏上答案态手动打(那时 body 没
  //      hw-printing,走正常打印,屏上副本可见)。
  const handlePrint = () => {
    document.body.classList.add('hw-printing');
    const cleanup = () => {
      document.body.classList.remove('hw-printing');
      window.removeEventListener('afterprint', cleanup);
    };
    window.addEventListener('afterprint', cleanup, { once: true });
    // window.print() 在大多数浏览器同步阻塞,返回时对话框已关。但 Firefox 某些版本
    // 是异步的,所以同时监听 afterprint 兜底。setTimeout 0 兜底:防止 print 抛错时
    // 类不被清掉(下次 Ctrl+P 仍污染)。
    try {
      window.print();
    } finally {
      // 同步路径兜底清理(afterprint 在 Chrome/Edge 可靠触发,这里双保险)。
      setTimeout(cleanup, 0);
    }
  };

  return (
    <>
      <Modal open={open} onClose={onClose} title="作业预览" size="xl">
        {/* 工具栏(no-print):admin 元信息 + 显示答案 toggle + 打印按钮 */}
        <div className="no-print mb-3 flex items-center justify-between gap-2 border-b border-border pb-3">
          <div className="text-xs text-muted">
            {view && (
              <>
                版本 v{view.version} · 生成于 {formatDate(view.created_at)} · {view.sections.length} 大题
              </>
            )}
          </div>
          <div className="flex items-center gap-2">
            <button
              className="btn-ghost btn-sm inline-flex items-center gap-1.5"
              onClick={() => setShowAnswers((v) => !v)}
              title="切换参考答案显示(家长/老师批改视角)"
            >
              {showAnswers ? <EyeOff size={14} /> : <Eye size={14} />}
              {showAnswers ? '隐藏答案' : '显示答案'}
            </button>
            <button
              className="btn-primary btn-sm inline-flex items-center gap-1.5"
              onClick={handlePrint}
              title="打印学生卷(不含参考答案)。如需打印答案版,关闭本弹窗后在作业页 Ctrl+P。"
            >
              <Printer size={14} />
              打印
            </button>
          </div>
        </div>

        {/* 屏上预览(.hw-screen-only):只在屏上显示,打印态 display:none(避免和打印副本重复)。
            注意这个不需要标 print-area,因为打印走下面的 portal 副本。 */}
        <div className="hw-screen-only">
          {viewQ.isLoading ? (
            <div className="flex items-center justify-center py-16">
              <Spinner size={24} />
            </div>
          ) : viewQ.isError ? (
            <div className="flex items-center justify-center py-16 text-sm text-bad">
              <AlertTriangle size={16} className="mr-2" />
              加载作业失败:{(viewQ.error as Error).message}
            </div>
          ) : view ? (
            <HomeworkPrintView
              view={view}
              courseTitle={courseTitle}
              subjectLabel={subjectLabel}
              episodeTitle={episodeTitle}
              showAnswers={showAnswers}
            />
          ) : null}
        </div>
      </Modal>

      {/* 打印专用副本:portal 到 document.body,屏上 display:none,打印态(body.hw-printing)
          display:block。这样打印时卷面是 body 的直接子节点(不在 Modal overlay 里),
          浏览器正常分页,不会出现"每页重复第一页"的 bug。
          **showAnswers 强制 false**:打印输出学生卷(无参考答案),独立于屏上 toggle。
          老师屏上开答案批改,打印仍出干净学生卷,不会泄答案给学生。只有 view 加载完成
          + Modal 打开时才渲染。 */}
      {open &&
        view &&
        createPortal(
          <div className="hw-print-portal">
            <HomeworkPrintView
              view={view}
              courseTitle={courseTitle}
              subjectLabel={subjectLabel}
              episodeTitle={episodeTitle}
              showAnswers={false}
            />
          </div>,
          document.body,
        )}
    </>
  );
}
