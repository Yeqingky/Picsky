import { Link } from 'react-router';
import { useTranslation } from 'react-i18next';
import { Button } from '../components/ui/button';

export default function NotFoundPage() {
  const { t } = useTranslation();

  return (
    <div className="bg-background flex min-h-screen flex-col items-center justify-center gap-3 p-6">
      <h1 className="font-heading text-muted-foreground text-4xl font-bold">
        {t('common.status404.title')}
      </h1>
      <p className="text-muted-foreground text-center text-base">
        {t('common.status404.description')}
      </p>
      <Button variant="outline" asChild>
        <Link to="/">{t('common.action.back')}</Link>
      </Button>
    </div>
  );
}
