// AI — providers, jobs (workflow queue), glossary candidate review,
// decision-trace runs, prompt preview, quiz observability, per-user study
// reports + advice, course summaries. The largest domain.

import { request, qs } from './_request';
import type {
  AiJobEnqueueResult,
  AiJobsResponse,
  AiModelsResult,
  AiProvider,
  AiProviderTestResult,
  AiQuizDetail,
  AiQuizRow,
  AiRealTestResult,
  AiRun,
  CourseSummaryAdmin,
  GlossaryCandidate,
  StudyAdviceRow,
  UserStudyReport,
} from '../types';

export const ai = {
  // AI provider CRUD + per-provider connectivity test. Mirrors the
  // storage-sources section: list/create/update/delete + a ping-style test.
  async listAiProviders(): Promise<AiProvider[]> {
    return request('/admin/api/ai/providers');
  },
  async createAiProvider(body: AiProvider): Promise<AiProvider> {
    return request('/admin/api/ai/providers', { method: 'POST', body: JSON.stringify(body) });
  },
  async updateAiProvider(id: number, body: AiProvider): Promise<AiProvider> {
    return request(`/admin/api/ai/providers/${id}`, { method: 'PUT', body: JSON.stringify(body) });
  },
  async testAiProvider(id: number): Promise<AiProviderTestResult> {
    return request(`/admin/api/ai/providers/${id}/test`, { method: 'POST' });
  },
  // Fetch the available model ids from an OpenAI-compatible relay. apiKey 可选:
  // 传 provider_id + 空 key 时后端用 DB 已存 key(edit 模式复用,不用反复重输 key)。
  // Returns {ok, models?, message?}.
  async fetchAiModels(baseUrl: string, apiKey: string, providerId?: number): Promise<AiModelsResult> {
    return request('/admin/api/ai/providers/models', {
      method: 'POST',
      body: JSON.stringify({ base_url: baseUrl, api_key: apiKey, provider_id: providerId }),
    });
  },
  // 实战测试:发一个接近真实 quiz 生成规模的长输出请求(max_tokens=6000),验证中转站
  // 能否扛住真实业务负载。暴露连通性测试测不出的长输出超时 502 故障,并启发式推测中转站
  // 后端模型。apiKey 可选:传 providerId + 空 key 时后端用 DB 已存 key。耗时较长(40-60s)。
  async realTestAiProvider(baseUrl: string, apiKey: string, modelName: string, providerId?: number): Promise<AiRealTestResult> {
    return request('/admin/api/ai/providers/test-real', {
      method: 'POST',
      body: JSON.stringify({ base_url: baseUrl, api_key: apiKey, model_name: modelName, provider_id: providerId }),
    });
  },

  // AI workflow jobs (slice/summarize/etc). The list endpoint rolls up status
  // counts alongside the jobs, so the page gets both in one round-trip.
  async enqueueAiJobs(jobType: string, episodeIds: number[]): Promise<AiJobEnqueueResult> {
    return request('/admin/api/ai/jobs', {
      method: 'POST',
      body: JSON.stringify({ job_type: jobType, episode_ids: episodeIds }),
    });
  },
  async listAiJobs(jobType?: string, status?: string): Promise<AiJobsResponse> {
    return request(`/admin/api/ai/jobs${qs({ job_type: jobType, status })}`);
  },
  // Manually reset one stuck 'processing' job back to 'queued' (the admin
  // counterpart of the automatic reaper). Throws on a 409 when the job isn't
  // currently processing (already finished or was reaped) — the caller surfaces
  // that as a benign "nothing to reset" toast.
  async resetAiJob(id: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/jobs/${id}/reset`, { method: 'POST' });
  },
  // Retry one 'failed' job: revive it back to 'queued' so the worker re-runs it.
  // Use case: the job failed (e.g. provider was misconfigured), the admin fixed
  // the underlying problem, now they want to re-run. Throws on a 409 when the
  // job isn't currently failed (already succeeded / was retried) — caller surfaces
  // that as a benign "nothing to retry" toast.
  async retryAiJob(id: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/jobs/${id}/retry`, { method: 'POST' });
  },
  // Skip polish: the polish-specific escape hatch. A failed polish job HALTS the
  // downstream chain (segment never auto-enqueues). When the admin decides the
  // raw subtitle is good enough (or the provider issue can't be fixed), this
  // marks the job done and chains segment so AI proceeds off the raw text.
  // Only valid on a FAILED POLISH job — throws 409 otherwise (caller surfaces
  // as a benign toast).
  async skipPolish(id: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/jobs/${id}/skip-polish`, { method: 'POST' });
  },
  // Acknowledge a failed job: flip failed→skipped WITHOUT re-running. For
  // unrecoverable failures admin can't fix (typical: episode has no subtitle →
  // summary/quiz fail). Throws 409 when the job isn't currently failed.
  async acknowledgeAiJob(id: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/jobs/${id}/acknowledge`, { method: 'POST' });
  },
  // --- PR2.5 glossary candidate review (术语候选审核) ---
  // The polish job mines term-correction rules; the admin reviews them here.
  // status: "" = all, "pending" = the default review list.
  async listGlossaryCandidates(courseId: number, status: string): Promise<GlossaryCandidate[]> {
    const q = status ? `?status=${encodeURIComponent(status)}` : '';
    return request(`/admin/api/courses/${courseId}/glossary-candidates${q}`);
  },
  async acceptGlossaryCandidate(
    id: number,
    body: { corrected?: string; context?: string; apply_to_subject_siblings?: boolean },
  ): Promise<{ ok: boolean }> {
    return request(`/admin/api/glossary-candidates/${id}/accept`, { method: 'POST', body: JSON.stringify(body) });
  },
  async rejectGlossaryCandidate(id: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/glossary-candidates/${id}/reject`, { method: 'POST' });
  },
  async acceptGlossaryCandidateBatch(
    ids: number[],
    applyToSubjectSiblings: boolean,
  ): Promise<{ accepted: number[]; errors: Record<number, string>; ok: boolean }> {
    return request('/admin/api/glossary-candidates/accept-batch', {
      method: 'POST',
      body: JSON.stringify({ ids, apply_to_subject_siblings: applyToSubjectSiblings }),
    });
  },
  // Decision-trace runs: the recorded model invocations an agent made. limit
  // caps the window (the page shows the most recent N).
  async listAiRuns(limit = 20): Promise<AiRun[]> {
    return request(`/admin/api/ai/runs${qs({ limit })}`);
  },
  /**
   * 预览某课程 + 某 agent 最终会拼出的完整 prompt(不调 LLM,纯文本拼接)。
   * 用于 admin 调优 hint 后立刻看效果,不用等真生成。返回 system_prompt +
   * user_prompt + resolved_hints(展示解析后的 5 个 hint 来源)。
   */
  async previewCoursePrompt(
    courseId: number,
    agent: 'summary' | 'quiz' | 'advice',
  ): Promise<{
    system_prompt: string;
    user_prompt: string;
    resolved_hints: {
      whisper_hint: string;
      summary_hint: string;
      quiz_hint: string;
      advice_hint: string;
      term_dict: string;
    };
  }> {
    return request(`/admin/api/ai/courses/${courseId}/preview-prompt`, {
      method: 'POST',
      body: JSON.stringify({ agent }),
    });
  },

  // Phase C — quiz observability. The admin reads a generated summary, lists a
  // user's quizzes, and drills into one quiz's full detail (questions + answers
  // + memory + the agent's reasoning trace + its feedback).
  async listUserQuizzes(userId: number): Promise<AiQuizRow[]> {
    return request(`/admin/api/ai/users/${userId}/quizzes`);
  },
  async getQuizDetail(quizId: number): Promise<AiQuizDetail> {
    return request(`/admin/api/ai/quizzes/${quizId}`);
  },

  // Phase E — admin 用户学习报告(跨课程画像,agent 驱动)。
  // triggerUserReport:触发生成(POST,异步入队 job),返回 status。
  // getUserReport:取报告(GET),status 三态(ready/generating/'')。
  async triggerUserReport(userId: number): Promise<{ status: string }> {
    return request(`/admin/api/ai/users/${userId}/study-report`, { method: 'POST' });
  },
  async getUserReport(userId: number): Promise<UserStudyReport> {
    return request(`/admin/api/ai/users/${userId}/study-report`);
  },

  // 课程级总结(admin 触发一次,所有学生共享)。POST 入队生成,DELETE 清掉重跑。
  async triggerCourseSummary(courseId: number): Promise<{ status: string }> {
    return request(`/admin/api/ai/courses/${courseId}/course-summary`, { method: 'POST' });
  },
  // getCourseSummary:GET 已生成的课程总结(三态 status + 文本 + 陈旧信号)。
  // 给内容管理 tab:(a) gate 删除按钮(无 summary 不显示);(b) 预览已生成的文本;
  // (c) 显示陈旧提示(current_episode_count > episode_count_at_gen)。
  async getCourseSummary(courseId: number): Promise<CourseSummaryAdmin> {
    return request(`/admin/api/ai/courses/${courseId}/course-summary`);
  },
  // listEpisodeSummaryStatus:某课程下哪些 episode 已有 summary。前端转 Set<number>
  // 用来 gate 内容管理 tab 每集的"删除"按钮(无 summary 不显示)。
  async listEpisodeSummaryStatus(courseId: number): Promise<number[]> {
    const r = await request<{ episode_ids_with_summary: number[] }>(
      `/admin/api/ai/courses/${courseId}/summaries-status`,
    );
    return r.episode_ids_with_summary ?? [];
  },

  // AI 控制台:重新生成 + 删除(2026-07-19 加)。
  // regenerateUserQuiz:对某用户的某集重跑 quiz agent(替换旧 quiz)。
  async regenerateUserQuiz(userId: number, episodeId: number): Promise<{ status: string }> {
    return request(`/admin/api/ai/users/${userId}/quizzes/regenerate`, {
      method: 'POST',
      body: JSON.stringify({ episode_id: episodeId }),
    });
  },
  // regenerateUserAdvice:按 scope 重跑 advice agent(episode/course/subject)。
  async regenerateUserAdvice(
    userId: number,
    scope: 'episode' | 'course' | 'subject',
    scopeId: number,
  ): Promise<{ status: string }> {
    return request(`/admin/api/ai/users/${userId}/advice/regenerate`, {
      method: 'POST',
      body: JSON.stringify({ scope, scope_id: scopeId }),
    });
  },
  // listUserAdvice:取某用户所有 scope 的 advice 原始行(StudyAdvice PascalCase-free model)。
  async listUserAdvice(userId: number): Promise<StudyAdviceRow[]> {
    return request(`/admin/api/ai/users/${userId}/advice`);
  },
  // deleteSummary:删某集的 summary(让 worker 下次重跑)。
  async deleteSummary(episodeId: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/summaries/${episodeId}`, { method: 'DELETE' });
  },
  // deleteAiQuiz:删某条 quiz(连同其 questions/answers/masteries/runs CASCADE)。
  async deleteAiQuiz(quizId: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/quizzes/${quizId}`, { method: 'DELETE' });
  },
  // deleteUserAdvice:按 scope+scope_id 删某用户的一条 advice。
  async deleteUserAdvice(
    userId: number,
    scope: 'episode' | 'course' | 'subject',
    scopeId: number,
  ): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/users/${userId}/advice${qs({ scope, scope_id: scopeId })}`, {
      method: 'DELETE',
    });
  },
  // deleteCourseSummary:删某课程的总总结(course-unique,删后下次重生成)。
  async deleteCourseSummary(courseId: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/courses/${courseId}/course-summary`, { method: 'DELETE' });
  },
  // deleteUserStudyReport:删某用户的跨课程学习报告(让其重新生成)。
  async deleteUserStudyReport(userId: number): Promise<{ ok: boolean }> {
    return request(`/admin/api/ai/users/${userId}/study-report`, { method: 'DELETE' });
  },
};
