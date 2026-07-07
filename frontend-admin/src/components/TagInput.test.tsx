import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { ToastProvider } from '../lib/toast';
import { TagInput } from './TagInput';

// TagInput uses ToastProvider via ImageUpload? No — TagInput itself doesn't,
// but rendering inside ToastProvider keeps tests honest if that changes.
function wrap(ui: React.ReactNode) {
  return render(<ToastProvider>{ui}</ToastProvider>);
}

describe('TagInput', () => {
  it('renders existing tags as chips', () => {
    wrap(<TagInput value="语文,数学" onChange={() => {}} />);
    expect(screen.getByText('语文')).toBeInTheDocument();
    expect(screen.getByText('数学')).toBeInTheDocument();
  });

  it('adds a tag on Enter', () => {
    const onChange = vi.fn();
    wrap(<TagInput value="" onChange={onChange} />);
    const input = screen.getByPlaceholderText('如：上学期,作文,重点');
    fireEvent.change(input, { target: { value: '重点' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(onChange).toHaveBeenCalledWith('重点');
  });

  it('adds a tag via the Add button', () => {
    const onChange = vi.fn();
    wrap(<TagInput value="" onChange={onChange} />);
    fireEvent.change(screen.getByPlaceholderText('如：上学期,作文,重点'), { target: { value: '作文' } });
    fireEvent.click(screen.getByText('添加'));
    expect(onChange).toHaveBeenCalledWith('作文');
  });

  it('ignores duplicate tags', () => {
    const onChange = vi.fn();
    wrap(<TagInput value="语文" onChange={onChange} />);
    fireEvent.change(screen.getByPlaceholderText('如：上学期,作文,重点'), { target: { value: '语文' } });
    fireEvent.keyDown(screen.getByPlaceholderText('如：上学期,作文,重点'), { key: 'Enter' });
    // Should not call onChange since '语文' already exists.
    expect(onChange).not.toHaveBeenCalled();
  });

  it('removes a tag via the × button', () => {
    const onChange = vi.fn();
    wrap(<TagInput value="语文,数学" onChange={onChange} />);
    // Click the first × (next to 语文).
    const removeButtons = screen.getAllByText('×');
    fireEvent.click(removeButtons[0]);
    expect(onChange).toHaveBeenCalled();
    // The new value should have one fewer tag.
    const calledWith = onChange.mock.calls[0][0];
    expect(calledWith.split(',')).toHaveLength(1);
    expect(calledWith).not.toContain('语文');
  });

  it('toggles suggestions from the pool', () => {
    const onChange = vi.fn();
    wrap(<TagInput value="" onChange={onChange} suggestions={['上学期', '作文']} />);
    // Both suggestions are unused (not in value), so both render as buttons.
    fireEvent.click(screen.getByText('上学期'));
    expect(onChange).toHaveBeenCalledWith('上学期');
  });

  it('hides suggestions already selected', () => {
    wrap(<TagInput value="上学期" onChange={() => {}} suggestions={['上学期', '作文']} />);
    // 作文 should appear as a clickable suggestion; 上学期 is already a chip.
    expect(screen.getByText('作文')).toBeInTheDocument();
    // The suggestions pool heading should not include 上学期.
    const suggestionButtons = screen.getAllByText('上学期');
    // Only the chip, no toggle button.
    expect(suggestionButtons).toHaveLength(1);
  });
});
