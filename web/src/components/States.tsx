import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from './ui/button';

export function EmptyState({
  title,
  description,
  action,
}: {
  title?: string;
  description?: ReactNode;
  action?: ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center justify-center gap-2 py-12 text-center">
      <p className="text-muted-foreground text-sm font-medium">
        {title ?? t('common.state.empty')}
      </p>
      {description && <p className="text-muted-foreground text-sm">{description}</p>}
      {action}
    </div>
  );
}

export function ErrorState({ message, onRetry }: { message?: string; onRetry?: () => void }) {
  const { t } = useTranslation();
  return (
    <div className="flex flex-col items-center gap-3 py-8">
      <p className="text-destructive text-sm font-medium">
        {message ?? t('common.state.loadFailed')}
      </p>
      {onRetry && (
        <Button variant="outline" size="sm" onClick={onRetry}>
          {t('common.action.retry')}
        </Button>
      )}
    </div>
  );
}
