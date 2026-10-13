import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import { zh } from './zh';

export const SUPPORTED_LANGUAGES = ['zh'] as const;
export type SupportedLanguage = (typeof SUPPORTED_LANGUAGES)[number];

export const DEFAULT_LANGUAGE: SupportedLanguage = 'zh';

void i18n.use(initReactI18next).init({
  resources: {
    zh: { translation: zh },
  },
  lng: DEFAULT_LANGUAGE,
  fallbackLng: DEFAULT_LANGUAGE,
  supportedLngs: [...SUPPORTED_LANGUAGES],
  interpolation: { escapeValue: false },
  returnNull: false,
});

export default i18n;
export { zh };
