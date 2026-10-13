import { useMemo, useState } from 'react';
import { Link, Outlet, useLocation, useNavigate } from 'react-router';
import {
  ChevronDown,
  ChevronUp,
  CloudUpload,
  Gauge,
  HardDrive,
  History,
  KeyRound,
  Images,
  LayoutDashboard,
  LogOut,
  Menu,
  Settings,
  ShieldCheck,
  User,
  Users,
  UsersRound,
  Wrench,
} from 'lucide-react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useAuth } from '../hooks/useAuth';
import { useSiteName } from '../hooks/useSite';
import { cravatarAvatarUrl } from '../utils/avatar';
import { isAdminVisible, hasPermission } from '../utils/permissions';
import type { PermissionName } from '../api/types';
import { ThemeModeToggleButton } from '../components/ThemeModeToggleButton';
import { Avatar, AvatarFallback, AvatarImage } from '../components/ui/avatar';
import { Button } from '../components/ui/button';
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from '../components/ui/collapsible';
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '../components/ui/dropdown-menu';
import { Sheet, SheetContent, SheetTitle } from '../components/ui/sheet';
import { cn } from '../lib/utils';

const DRAWER_WIDTH = 208;

interface NavItem {
  key: string;
  labelKey: string;
  path: string;
  icon: ReactNode;
  permission?: PermissionName;
  children?: NavItem[];
}

const ICON_CLASS = 'size-4 shrink-0';

function buildNavItems(permissions: readonly string[]): NavItem[] {
  const items: NavItem[] = [
    {
      key: 'dashboard',
      labelKey: 'nav.dashboard',
      path: '/',
      icon: <Gauge className={ICON_CLASS} />,
    },
    {
      key: 'upload',
      labelKey: 'nav.upload',
      path: '/upload',
      icon: <CloudUpload className={ICON_CLASS} />,
    },
    {
      key: 'gallery',
      labelKey: 'nav.gallery',
      path: '/gallery',
      icon: <Images className={ICON_CLASS} />,
    },
    {
      key: 'keys',
      labelKey: 'nav.keys',
      path: '/keys',
      icon: <KeyRound className={ICON_CLASS} />,
      permission: 'manage_keys',
    },
    {
      key: 'profile',
      labelKey: 'nav.profile',
      path: '/profile',
      icon: <User className={ICON_CLASS} />,
    },
  ];

  if (!isAdminVisible(permissions)) return items;

  const adminChildrenAll: NavItem[] = [
    {
      key: 'admin-overview',
      labelKey: 'nav.adminOverview',
      path: '/admin',
      icon: <LayoutDashboard className={ICON_CLASS} />,
    },
    {
      key: 'admin-users',
      labelKey: 'nav.adminUsers',
      path: '/admin/users',
      icon: <UsersRound className={ICON_CLASS} />,
      permission: 'manage_users',
    },
    {
      key: 'admin-groups',
      labelKey: 'nav.adminGroups',
      path: '/admin/groups',
      icon: <Users className={ICON_CLASS} />,
      permission: 'manage_groups',
    },
    {
      key: 'admin-policies',
      labelKey: 'nav.adminPolicies',
      path: '/admin/policies',
      icon: <HardDrive className={ICON_CLASS} />,
      permission: 'manage_policies',
    },
    {
      key: 'admin-settings',
      labelKey: 'nav.adminSettings',
      path: '/admin/settings',
      icon: <Settings className={ICON_CLASS} />,
      permission: 'manage_settings',
    },
    {
      key: 'admin-tasks',
      labelKey: 'nav.adminTasks',
      path: '/admin/tasks',
      icon: <Wrench className={ICON_CLASS} />,
      permission: 'manage_settings',
    },
    {
      key: 'admin-logs',
      labelKey: 'nav.adminLogs',
      path: '/admin/logs',
      icon: <History className={ICON_CLASS} />,
      permission: 'view_logs',
    },
  ];
  const adminChildren = adminChildrenAll.filter(
    (item) => !item.permission || hasPermission(permissions, item.permission),
  );

  items.push({
    key: 'admin',
    labelKey: 'nav.admin',
    path: '/admin',
    icon: <ShieldCheck className={ICON_CLASS} />,
    children: adminChildren,
  });

  return items;
}

const navLinkClass = (selected: boolean) =>
  cn(
    'flex min-h-9 items-center gap-2.5 rounded-lg px-3 text-sm transition-colors',
    'hover:bg-accent hover:text-accent-foreground',
    selected ? 'bg-accent font-medium text-accent-foreground' : 'text-foreground',
  );

function NavList({ items, onNavigate }: { items: NavItem[]; onNavigate?: () => void }) {
  const { t } = useTranslation();
  const location = useLocation();

  return (
    <nav className="flex flex-col gap-0.5 px-2">
      {items.map((item) => {
        if (!item.children) {
          return (
            <Link
              key={item.key}
              to={item.path}
              onClick={onNavigate}
              className={navLinkClass(location.pathname === item.path)}
            >
              <span className="text-muted-foreground [&_svg]:size-4">{item.icon}</span>
              {t(item.labelKey)}
            </Link>
          );
        }

        const childSelected = item.children.some((child) =>
          location.pathname.startsWith(child.path),
        );
        return (
          <NavGroup
            key={item.key}
            item={item}
            defaultOpen={childSelected}
            onNavigate={onNavigate}
          />
        );
      })}
    </nav>
  );
}

function NavGroup({
  item,
  defaultOpen,
  onNavigate,
}: {
  item: NavItem;
  defaultOpen: boolean;
  onNavigate?: () => void;
}) {
  const { t } = useTranslation();
  const location = useLocation();
  const [open, setOpen] = useState(defaultOpen);

  return (
    <Collapsible open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger asChild>
        <button type="button" className={cn(navLinkClass(false), 'w-full text-left')}>
          <span className="text-muted-foreground [&_svg]:size-4">{item.icon}</span>
          <span className="flex-1">{t(item.labelKey)}</span>
          {open ? (
            <ChevronUp className="text-muted-foreground size-4" />
          ) : (
            <ChevronDown className="text-muted-foreground size-4" />
          )}
        </button>
      </CollapsibleTrigger>
      <CollapsibleContent>
        <div className="flex flex-col gap-0.5 pl-4">
          {item.children?.map((child) => (
            <Link
              key={child.key}
              to={child.path}
              onClick={onNavigate}
              className={cn(
                navLinkClass(location.pathname === child.path),
                'min-h-8 text-muted-foreground hover:text-accent-foreground',
                location.pathname === child.path && 'text-accent-foreground',
              )}
            >
              <span className="[&_svg]:size-4">{child.icon}</span>
              {t(child.labelKey)}
            </Link>
          ))}
        </div>
      </CollapsibleContent>
    </Collapsible>
  );
}

function UserMenu() {
  const { t } = useTranslation();
  const { user, logout, permissions } = useAuth();
  const navigate = useNavigate();
  const [open, setOpen] = useState(false);
  const avatarUrl = useMemo(() => cravatarAvatarUrl(user?.email), [user?.email]);

  const handleLogout = async () => {
    setOpen(false);
    await logout();
    navigate('/login', { replace: true });
  };

  return (
    <DropdownMenu open={open} onOpenChange={setOpen}>
      <DropdownMenuTrigger asChild>
        <button
          type="button"
          aria-label={t('nav.userMenu')}
          className="hover:bg-accent rounded-full p-0.5 transition-colors"
        >
          <Avatar className="size-8">
            {avatarUrl && <AvatarImage src={avatarUrl} alt="" />}
            <AvatarFallback className="text-xs">
              {(user?.nickname || user?.username || '?').slice(0, 1).toUpperCase()}
            </AvatarFallback>
          </Avatar>
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-52">
        <DropdownMenuItem
          onClick={() => {
            setOpen(false);
            navigate('/profile');
          }}
        >
          <User />
          {t('nav.profile')}
        </DropdownMenuItem>
        {hasPermission(permissions, 'manage_keys') && (
          <DropdownMenuItem
            onClick={() => {
              setOpen(false);
              navigate('/keys');
            }}
          >
            <KeyRound />
            {t('nav.keys')}
          </DropdownMenuItem>
        )}
        <DropdownMenuSeparator />
        <DropdownMenuItem variant="destructive" onClick={() => void handleLogout()}>
          <LogOut />
          {t('common.action.logout')}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function SidebarContent({ onNavigate }: { onNavigate?: () => void }) {
  const { permissions } = useAuth();
  const siteName = useSiteName();
  const navItems = useMemo(() => buildNavItems(permissions), [permissions]);

  return (
    <div className="flex h-full flex-col">
      <div className="flex h-14 min-w-0 items-center px-4 md:h-16">
        <span className="font-heading truncate text-base font-bold">{siteName}</span>
      </div>
      <div className="border-b" />
      <div className="flex-1 overflow-y-auto py-2">
        <NavList items={navItems} onNavigate={onNavigate} />
      </div>
    </div>
  );
}

export function AppLayout() {
  const { t } = useTranslation();
  const siteName = useSiteName();
  const [mobileOpen, setMobileOpen] = useState(false);

  return (
    <div className="bg-background flex min-h-svh w-full">
      <div className="hidden md:block md:w-[236px] md:shrink-0">
        <div
          className="fixed inset-y-0 left-0 hidden shrink-0 border-r bg-background md:block"
          style={{ width: DRAWER_WIDTH }}
        >
          <SidebarContent />
        </div>
      </div>

      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <SheetContent side="left" className="gap-0 p-0" style={{ width: DRAWER_WIDTH }}>
          <SheetTitle className="sr-only">{siteName}</SheetTitle>
          <SidebarContent onNavigate={() => setMobileOpen(false)} />
        </SheetContent>
      </Sheet>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="bg-background/80 sticky top-0 z-40 flex h-14 items-center gap-2 border-b px-4 backdrop-blur md:h-16 md:px-6">
          <Button
            variant="ghost"
            size="icon"
            className="md:hidden"
            aria-label={t('common.a11y.openMenu')}
            onClick={() => setMobileOpen(true)}
          >
            <Menu />
          </Button>
          <div className="flex-1" />
          <ThemeModeToggleButton />
          <UserMenu />
        </header>

        <main className="mx-auto flex w-full max-w-[1600px] flex-1 flex-col p-4 md:p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
