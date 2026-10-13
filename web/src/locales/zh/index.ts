import { common } from './common';
import { nav } from './nav';
import { dashboard } from './dashboard';
import { permissions } from './permissions';
import { auth } from './auth';
import { account } from './account';
import { upload } from './upload';
import { gallery } from './gallery';
import { admin } from './admin';
import { adminPolicies } from './adminPolicies';
import { adminSystem } from './adminSystem';

/** 首版仅中文; 结构按命名空间划分, 追加语言只需新增同构资源目录 */
export const zh = {
  common,
  nav,
  dashboard,
  permissions,
  auth,
  account,
  upload,
  gallery,
  admin,
  adminPolicies,
  adminSystem,
};

export type ZhResources = typeof zh;
