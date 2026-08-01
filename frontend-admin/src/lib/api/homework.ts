import { request, qs } from './_request';
import type { Homework, HomeworkPromptConfig, HomeworkView } from '../types';

// homework.ts — 作业卷 API。
//
// standalone /admin/homework 页已删,生成入口并入 AI 控制台 RegenTab(勾选课时 →
// 批量入队)。本文件保留,因为 RegenTab 的 CourseRegenColumn + 预览 Modal 还用
// homeworkList/homeworkGet/homeworkGetPrompt 等。卷面渲染在
// components/homework/HomeworkPrintView.tsx(屏上预览 + 打印共用)。
//
// 单/批量 episode 入队走通用 enqueueAiJobs('homework', ids)(见 ai.ts),本文件不提供
// 专门的入队方法——保持和 segment/summary/polish 三兄弟一致的 API 形态。
//
// JSON 契约已冻结(全部 snake_case)。本文件只是 request 的薄封装,与 exam.ts /
// wrongbook.ts 范式一致。注意:
//   - homeworkGenerate(course-level 整门课)**已废弃**(v2 标注,二期清):前端不再用它,
//     保留仅为兜底/向后兼容。新生成入口走 enqueueAiJobs('homework', episodeIds)。
//   - prompt 端点带 ?key=<subject_key> query,后端用 key 选 prompt 模板分支(math 有
//     自己的、english 有自己的……)。

export const homework = {
  /** [DEPRECATED v2] 批量生成该课程所有 episode 的作业(course-level 整门课)。
   *  v2 起前端改用勾选式:enqueueAiJobs('homework', episodeIds)(见 ai.ts)。本方法保留
   *  仅为兜底/向后兼容,二期清理时连后端端点一起删。返回 {enqueued: number}(本次新入队
   *  的集数,已有在途作业的集被自动跳过)。 */
  async homeworkGenerate(courseId: number): Promise<{ enqueued: number }> {
    return request(`/admin/api/ai/courses/${courseId}/homework/generate`, { method: 'POST' });
  },

  /** 列该课程所有作业(active + archived)。每条是 Homework 列表项(无 sections/questions)。 */
  async homeworkList(courseId: number): Promise<Homework[]> {
    return request(`/admin/api/ai/courses/${courseId}/homeworks`);
  },

  /** 取单份作业完整内容(预览/打印)。后端找不到时返回 404,Request 会抛 ApiError。 */
  async homeworkGet(id: number): Promise<HomeworkView> {
    return request(`/admin/api/ai/homeworks/${id}`);
  },

  /** 取某 subject 的 prompt 配置(首次 GET 时后端 lazy 灌默认,所以一定有返回)。 */
  async homeworkGetPrompt(subjectId: number, key: string): Promise<HomeworkPromptConfig> {
    return request(`/admin/api/ai/subjects/${subjectId}/homework-prompt${qs({ key })}`);
  },

  /** 覆盖某 subject 的 prompt。body {system_prompt: string}(必填)。 */
  async homeworkSavePrompt(subjectId: number, key: string, prompt: string): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/subjects/${subjectId}/homework-prompt${qs({ key })}`, {
      method: 'PUT',
      body: JSON.stringify({ system_prompt: prompt }),
    });
  },

  /** 重置某 subject 的 prompt 回默认。 */
  async homeworkResetPrompt(subjectId: number, key: string): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/subjects/${subjectId}/homework-prompt/reset${qs({ key })}`, {
      method: 'POST',
    });
  },
};
