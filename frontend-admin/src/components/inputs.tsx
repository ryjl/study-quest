import { useRef, useState } from 'react';
import { api } from '../lib/api';
import { useToast } from '../lib/toast';
import { GRADES } from '../lib/types';

// Image upload with URL input + local file picker, posts to /admin/api/upload/image.
export function ImageUpload({
  value,
  onChange,
  label = '封面图',
}: {
  value: string;
  onChange: (v: string) => void;
  label?: string;
}) {
  const fileRef = useRef<HTMLInputElement>(null);
  const { error } = useToast();
  const [uploading, setUploading] = useState(false);

  const upload = async (file: File) => {
    setUploading(true);
    try {
      const { url } = await api.uploadImage(file);
      onChange(url);
    } catch (e) {
      error('图片上传失败: ' + (e as Error).message);
    } finally {
      setUploading(false);
    }
  };

  return (
    <div>
      <label className="mb-1 block text-xs text-muted">{label}</label>
      <div className="flex gap-2">
        <input className="input" placeholder="输入 URL 或本地上传" value={value} onChange={(e) => onChange(e.target.value)} />
        <button type="button" className="btn-secondary whitespace-nowrap" onClick={() => fileRef.current?.click()} disabled={uploading}>
          {uploading ? '上传中...' : '本地上传'}
        </button>
        <input
          ref={fileRef}
          type="file"
          accept="image/*"
          className="hidden"
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (f) upload(f);
            e.target.value = '';
          }}
        />
      </div>
      {value && (
        <div className="mt-2 inline-block overflow-hidden rounded-lg border border-border">
          <img src={value} alt="预览" className="h-20 w-32 object-cover" onError={(e) => ((e.target as HTMLImageElement).style.opacity = '0.3')} />
        </div>
      )}
    </div>
  );
}

// Multi-select grade checkbox grid.
export function GradePicker({
  value,
  onChange,
}: {
  value: string; // comma-separated
  onChange: (v: string) => void;
}) {
  // Filter empties so a blank value yields [] (not [""]) — otherwise the
  // first toggle produces a leading comma like ",3" which the backend rejects.
  const selected = value.split(',').map((s) => s.trim()).filter((s) => s.length > 0);

  const toggle = (key: string) => {
    if (selected.includes(key)) {
      onChange(selected.filter((k) => k !== key).join(','));
    } else {
      onChange([...selected, key].join(','));
    }
  };

  return (
    <div className="grid grid-cols-2 gap-2 rounded-xl border border-border bg-card-2 p-3">
      {GRADES.map((g) => (
        <label key={g.key} className="flex items-center gap-2 text-sm" style={{ gridColumn: g.key === 'universal' ? 'span 2' : undefined }}>
          <input type="checkbox" checked={selected.includes(g.key)} onChange={() => toggle(g.key)} className="h-4 w-4 accent-primary" />
          {g.name}
        </label>
      ))}
    </div>
  );
}
