import { Monitor, Moon, Sun } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { Button } from './ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from './ui/tooltip';
import { useThemeMode } from '../theme/ThemeModeProvider';
import type { ThemeMode } from '../theme';

const MODE_LABEL_KEY: Record<ThemeMode, string> = {
  light: 'common.theme.light',
  dark: 'common.theme.dark',
  system: 'common.theme.system',
};

/** 点击循环顺序: 亮色 -> 暗色 -> 跟随系统 */
const NEXT_MODE: Record<ThemeMode, ThemeMode> = {
  light: 'dark',
  dark: 'system',
  system: 'light',
};

/** 主题切换 (点击循环 亮色 / 暗色 / 跟随系统), 登录页与顶栏共用 */
export function ThemeModeToggleButton() {
  const { t } = useTranslation();
  const { mode, setMode } = useThemeMode();

  const icon =
    mode === 'light' ? (
      <Sun className="size-4" />
    ) : mode === 'dark' ? (
      <Moon className="size-4" />
    ) : (
      <Monitor className="size-4" />
    );

  const label = `${t('common.theme.label')}: ${t(MODE_LABEL_KEY[mode])}`;

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="icon"
          aria-label={label}
          onClick={() => setMode(NEXT_MODE[mode])}
        >
          {icon}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}
