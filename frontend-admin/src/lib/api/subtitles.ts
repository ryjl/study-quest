// Subtitle track CRUD + PR3 embedded-stream extraction.
// extractSubtitle: ffmpeg -map 0:streamIndex -c:s webvtt. streamIndex comes
// from the probed media_meta_json.streams[].index. Server returns 400 with a
// "use Whisper" hint for bitmap codecs (PGS/VOBSUB/DVB); that surfaces via the
// standard ApiError message path and shows in the toast.

import { request } from './_request';
import type { Subtitle, SubtitleDetail } from '../types';

export const subtitles = {
  async listSubtitles(episodeId: number): Promise<Subtitle[]> {
    return request(`/admin/api/episodes/${episodeId}/subtitles`);
  },
  async getSubtitle(id: number): Promise<SubtitleDetail> {
    return request(`/admin/api/subtitles/${id}`);
  },
  async saveSubtitle(
    episodeId: number,
    body: { language: string; label: string; srt_content: string },
  ): Promise<{ status: string }> {
    return request(`/admin/api/episodes/${episodeId}/subtitles`, { method: 'POST', body: JSON.stringify(body) });
  },
  async deleteSubtitle(id: number): Promise<{ status: string }> {
    return request(`/admin/api/subtitles/${id}`, { method: 'DELETE' });
  },
  async extractSubtitle(
    episodeId: number,
    body: { stream_index: number; language: string; label: string },
  ): Promise<{ status: string }> {
    return request(`/admin/api/episodes/${episodeId}/extract-subtitle`, {
      method: 'POST',
      body: JSON.stringify(body),
    });
  },
};
