// MiniMarkdown — 极轻量的 markdown 渲染器,不引第三方库(项目风格:不引 recharts/log 库)。
// 只处理 AI 输出最常见的格式:标题(#/##/###)、粗体(**x**)、斜体(*x* / _x_)、
// 行内代码(`x`)、无序列表(- / *)、有序列表(1.)、段落/换行。够用即可,不追求完整
// CommonMark 合规——AI 的 advice/report 输出格式可控,这几类覆盖 95% 场景。
//
// 用在哪:学生建议、学习报告、agent 评价等 AI 生成的自然语言文本。原来全用
// whitespace-pre-wrap 纯文本渲染,粗体/列表/标题都看不出结构,可读性差。

import { type ReactNode } from 'react';

// 行内格式:粗体/斜体/代码。按顺序处理(粗体先于斜体,避免 ** 被斜体吃掉)。
function renderInline(text: string, keyPrefix: string): ReactNode[] {
  const nodes: ReactNode[] = [];
  // 用占位符分阶段替换,避免正则交叉干扰。
  // 1. 行内代码 `code`(最先,内容不解析其它格式)
  const segments: { text: string; code?: boolean }[] = [{ text }];
  const codeParts: string[] = [];
  let working = text.replace(/`([^`]+)`/g, (_m, code: string) => {
    codeParts.push(code);
    return `\u0000CODE${codeParts.length - 1}\u0000`;
  });
  // 2. 粗体 **x**(用占位,后面统一渲染)
  const boldParts: string[] = [];
  working = working.replace(/\*\*([^*]+)\*\*/g, (_m, b: string) => {
    boldParts.push(b);
    return `\u0000BOLD${boldParts.length - 1}\u0000`;
  });
  // 3. 斜体 *x* 或 _x_
  const italicParts: string[] = [];
  working = working.replace(/(?:\*([^*]+)\*|_([^_]+)_)/g, (_m, i1: string, i2: string) => {
    italicParts.push(i1 || i2);
    return `\u0000ITALIC${italicParts.length - 1}\u0000`;
  });

  // 把占位符切分成节点。按 \u0000TAG\d 模式分词。
  const tokenRegex = /\u0000(CODE|BOLD|ITALIC)(\d+)\u0000/;
  let rest = working;
  let idx = 0;
  while (rest.length > 0) {
    const m = rest.match(tokenRegex);
    if (!m) {
      nodes.push(rest);
      break;
    }
    const before = rest.slice(0, m.index);
    if (before) nodes.push(before);
    const tag = m[1];
    const i = Number(m[2]);
    const key = `${keyPrefix}-${idx++}`;
    if (tag === 'CODE') {
      nodes.push(<code key={key} className="rounded bg-card-2 px-1 py-0.5 font-mono text-[0.85em] text-txt">{codeParts[i]}</code>);
    } else if (tag === 'BOLD') {
      nodes.push(<strong key={key} className="font-semibold text-txt">{boldParts[i]}</strong>);
    } else if (tag === 'ITALIC') {
      nodes.push(<em key={key}>{italicParts[i]}</em>);
    }
    rest = rest.slice((m.index ?? 0) + m[0].length);
  }
  // 清理:如果整段没有占位符(纯文本),上面的循环把 rest 整个 push 了。这里兜底
  // 处理 segments[0].code(行内代码优先拆分)——实际上 code 已经在占位符阶段处理,
  // segments 变量保留仅为可读性,不再使用。
  void segments;
  return nodes;
}

export function MiniMarkdown({ text, className = '' }: { text: string; className?: string }) {
  if (!text) return null;
  const lines = text.split('\n');
  const blocks: ReactNode[] = [];
  let listItems: string[] = [];
  let listOrdered = false;

  // 把累积的列表项 flush 成一个 <ul>/<ol>。
  const flushList = (key: number) => {
    if (listItems.length === 0) return;
    const items = listItems.map((item, i) => (
      <li key={i} className="ml-4">{renderInline(item, `li-${key}-${i}`)}</li>
    ));
    blocks.push(
      listOrdered ? (
        <ol key={`ol-${key}`} className="my-1 list-decimal space-y-0.5">{items}</ol>
      ) : (
        <ul key={`ul-${key}`} className="my-1 list-disc space-y-0.5">{items}</ul>
      ),
    );
    listItems = [];
  };

  lines.forEach((raw, i) => {
    const line = raw.trimEnd();
    // 空行:flush 当前列表,段落间断。
    if (line.trim() === '') {
      flushList(i);
      return;
    }
    // 标题:# / ## / ###
    const headingMatch = line.match(/^(#{1,3})\s+(.+)$/);
    if (headingMatch) {
      flushList(i);
      const level = headingMatch[1].length;
      const cls = level === 1 ? 'text-base font-semibold mt-2 mb-1' : level === 2 ? 'text-sm font-semibold mt-2 mb-1' : 'text-sm font-medium mt-1.5 mb-0.5';
      blocks.push(<div key={`h-${i}`} className={cls}>{renderInline(headingMatch[2], `h-${i}`)}</div>);
      return;
    }
    // 无序列表:- 或 * 开头
    const ulMatch = line.match(/^\s*[-*]\s+(.+)$/);
    if (ulMatch) {
      if (listOrdered) flushList(i); // 切换列表类型
      listOrdered = false;
      listItems.push(ulMatch[1]);
      return;
    }
    // 有序列表:1. / 2. 开头
    const olMatch = line.match(/^\s*\d+\.\s+(.+)$/);
    if (olMatch) {
      if (!listOrdered) flushList(i);
      listOrdered = true;
      listItems.push(olMatch[1]);
      return;
    }
    // 普通段落
    flushList(i);
    blocks.push(<p key={`p-${i}`} className="my-1 leading-relaxed">{renderInline(line, `p-${i}`)}</p>);
  });
  flushList(lines.length);

  return <div className={`text-sm text-txt ${className}`}>{blocks}</div>;
}
