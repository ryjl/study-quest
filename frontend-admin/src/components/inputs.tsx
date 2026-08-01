import { useRef, useState } from 'react';
import { api } from '../lib/api';
import { useToast } from '../lib/toast';

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

// Multi-select grade tag picker. "5 预设 checkbox + 自定义 tag 输入框"模式
// (后端 grade 是开放 tag 体系)。
//
// 行为:
//   - 5 个预设(primary/junior/senior/adult/universal)以 checkbox 形式展示
//   - 已选的非预设值(如老 DB 数据"3"、自定义"考研")以可删除 chip 形式回显
//   - 底部输入框可追加任意自定义 tag,按回车或点击"添加"提交
import { PRESET_GRADES, gradeLabel } from '../lib/types';

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
  const [customInput, setCustomInput] = useState('');

  const presetKeys = new Set(PRESET_GRADES.map((g) => g.key));
  const customTags = selected.filter((k) => !presetKeys.has(k));

  const toggle = (key: string) => {
    if (selected.includes(key)) {
      onChange(selected.filter((k) => k !== key).join(','));
    } else {
      onChange([...selected, key].join(','));
    }
  };

  const addCustom = () => {
    const v = customInput.trim();
    if (!v || selected.includes(v)) {
      setCustomInput('');
      return;
    }
    onChange([...selected, v].join(','));
    setCustomInput('');
  };

  const removeCustom = (key: string) => {
    onChange(selected.filter((k) => k !== key).join(','));
  };

  return (
    <div className="rounded-lg border border-border bg-card-2 p-3 space-y-3">
      <div className="grid grid-cols-2 gap-2">
        {PRESET_GRADES.map((g) => (
          <label
            key={g.key}
            className="flex items-center gap-2 text-sm"
            style={{ gridColumn: g.key === 'universal' ? 'span 2' : undefined }}
          >
            <input
              type="checkbox"
              checked={selected.includes(g.key)}
              onChange={() => toggle(g.key)}
              className="h-4 w-4 accent-primary"
            />
            {g.name}
          </label>
        ))}
      </div>

      {customTags.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {customTags.map((k) => (
            <span
              key={k}
              className="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2 py-0.5 text-xs text-primary"
            >
              {gradeLabel(k)}
              <button
                type="button"
                onClick={() => removeCustom(k)}
                className="text-primary/60 hover:text-primary"
                aria-label={`移除 ${k}`}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}

      <div className="flex gap-2">
        <input
          type="text"
          value={customInput}
          onChange={(e) => setCustomInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              e.preventDefault();
              addCustom();
            }
          }}
          placeholder="自定义 tag(如 考研、职场、幼小衔接)"
          className="input flex-1 text-sm"
        />
        <button type="button" onClick={addCustom} className="btn-secondary text-sm">
          添加
        </button>
      </div>
    </div>
  );
}
