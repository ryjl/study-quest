// CourseQualityTab — 课程工作台「质量」tab。
//
// 诚实设计:错题/考试统计端点是全局聚合,不支持按 course 过滤(零后端改动原则)。
// 所以这里:顶部提示框说明"按本课过滤需看每集 quiz 的答题情况",下方复用全局
// WrongBook + Exam 观测,作为"共性题面/难度问题"的参考——发现全员高频错题,
// 可去「内容」tab 重新生成相关总结/调整 Prompt。
import { useSearchParams } from 'react-router-dom';
import { Info } from 'lucide-react';
import { WrongBook } from '../../WrongBook';
import { Exam } from '../../Exam';

export function CourseQualityTab({ courseId: _courseId }: { courseId: number }) {
  const [, setParams] = useSearchParams();
  return (
    <div className="space-y-4">
      <div className="flex items-start gap-2.5 rounded-lg border border-blue-500/30 bg-blue-500/5 p-3">
        <Info size={15} className="mt-0.5 shrink-0 text-blue-600" />
        <div className="text-xs text-txt">
          <p className="font-medium">发现题面或难度问题?</p>
          <p className="mt-0.5 text-muted">
            下方是全局错题/考试观测(全员共性)。高频错题往往是题面有问题或知识点太难——可以去{' '}
            <button className="text-primary underline-offset-2 hover:underline" onClick={() => setParams({ tab: 'content' }, { replace: true })}>
              内容 tab
            </button>{' '}
            重新生成相关总结,或在{' '}
            <button className="text-primary underline-offset-2 hover:underline" onClick={() => setParams({ tab: 'prompt' }, { replace: true })}>
              Prompt tab
            </button>{' '}
            调整出题指引。
          </p>
        </div>
      </div>
      <WrongBook embedded />
      <Exam embedded />
    </div>
  );
}
