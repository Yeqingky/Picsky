import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { X } from 'lucide-react';
import { cn } from '../lib/utils';
import { Badge } from './ui/badge';
import { Button } from './ui/button';
import { Input } from './ui/input';

export function TagsInput({
  id,
  value,
  onChange,
  placeholder,
  invalid = false,
}: {
  id: string;
  value: string[];
  onChange: (value: string[]) => void;
  placeholder: string;
  invalid?: boolean;
}) {
  const { t } = useTranslation();
  const [draft, setDraft] = useState('');

  const addTags = (raw: string) => {
    const additions = raw
      .split(/[,;\n]/)
      .map((tag) => tag.trim())
      .filter((tag) => tag.length > 0);
    if (additions.length > 0) {
      const seen = new Set(value.map((tag) => tag.toLowerCase()));
      const next = [...value];
      for (const tag of additions) {
        const normalized = tag.toLowerCase();
        if (seen.has(normalized)) continue;
        seen.add(normalized);
        next.push(tag);
      }
      if (next.length !== value.length) onChange(next);
    }
    setDraft('');
  };

  const handleChange = (raw: string) => {
    const parts = raw.split(/[,;\n]/);
    if (parts.length === 1) {
      setDraft(raw);
      return;
    }
    addTags(parts.slice(0, -1).join(','));
    setDraft(parts[parts.length - 1] ?? '');
  };

  return (
    <div
      aria-invalid={invalid || undefined}
      className={cn(
        'border-input flex min-h-8 w-full min-w-0 flex-wrap items-center gap-1.5 rounded-lg border bg-transparent px-2 py-1 transition-colors focus-within:border-ring focus-within:ring-3 focus-within:ring-ring/50 aria-invalid:border-destructive aria-invalid:ring-3 aria-invalid:ring-destructive/20 dark:bg-input/30',
      )}
    >
      {value.map((tag, index) => (
        <Badge key={`${tag}-${index}`} variant="secondary" className="max-w-full gap-0.5 pr-0.5">
          <span className="min-w-0 truncate" title={tag}>
            {tag}
          </span>
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            className="size-4 rounded-full"
            aria-label={`${t('common.action.remove')} ${tag}`}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => onChange(value.filter((_, itemIndex) => itemIndex !== index))}
          >
            <X />
          </Button>
        </Badge>
      ))}
      <Input
        id={id}
        type="text"
        autoComplete="off"
        aria-invalid={invalid || undefined}
        value={draft}
        placeholder={placeholder}
        onChange={(event) => handleChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ',' || event.key === ';') {
            event.preventDefault();
            addTags(draft);
          } else if (event.key === 'Backspace' && draft === '' && value.length > 0) {
            event.preventDefault();
            onChange(value.slice(0, -1));
          }
        }}
        onBlur={() => addTags(draft)}
        className="h-6 w-auto min-w-[8rem] flex-1 border-0 bg-transparent px-1 py-0 shadow-none focus-visible:border-0 focus-visible:ring-0 dark:bg-transparent"
      />
    </div>
  );
}
