import { request, qs } from './_request';
import type { Homework, HomeworkPromptConfig, HomeworkView } from '../types';

// homework.ts — 作业卷 API for the /admin/homework page。
//
// 后端 Stage 1 已完成,JSON 契约已冻结(全部 snake_case)。本文件只是 request 的薄
// 封装,与 exam.ts / wrongbook.ts 范式一致。注意:
//   - 没有"单 episode 重生成"端点。单集重生成 = 用户再点一次"批量生成",后端去重
//     门保证只补缺的集。前端不要加单集重生成按钮。
//   - prompt 端点带 ?key=<subject_key> query,后端用 key 选 prompt 模板分支(math 有
//     自己的、english 有自己的……)。

export const homework = {
  /** 批量生成该课程所有 episode 的作业。返回 {enqueued: number}(本次新入队的集数,
   *  已有在途作业的集被自动跳过)。后端是幂等的:重复点只会补缺的集。 */
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
