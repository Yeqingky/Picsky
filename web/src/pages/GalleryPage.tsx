import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Check, LayoutGrid, List } from 'lucide-react';
import { useInView } from 'react-intersection-observer';
import { Virtuoso } from 'react-virtuoso';
import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import { useSearchParams } from 'react-router';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { imagesApi } from '../api';
import type { ImageRecord } from '../api/types';
import type { GalleryView } from '../utils/constants';
import { PageHeader } from '../components/PageHeader';
import { EmptyState, ErrorState } from '../components/States';
import { MasonryGrid } from '../components/MasonryGrid';
import { MasonrySkeleton, SkeletonSurface, TableSkeleton } from '../components/skeletons';
import { GalleryImageCard } from '../components/gallery/GalleryImageCard';
import { GalleryFilters } from '../components/gallery/GalleryFilters';
import type { GalleryFiltersValue } from '../components/gallery/GalleryFilters';
import { GalleryListView } from '../components/gallery/GalleryListView';
import { GalleryBatchBar } from '../components/gallery/GalleryBatchBar';
import { ImageDetailDrawer } from '../components/gallery/ImageDetailDrawer';
import { useConfirm } from '../components/ConfirmDialog';
import { Alert, AlertDescription } from '../components/ui/alert';
import { Badge } from '../components/ui/badge';
import { Button } from '../components/ui/button';
import { Skeleton } from '../components/ui/skeleton';
import { Toggle } from '../components/ui/toggle';
import { ToggleGroup, ToggleGroupItem } from '../components/ui/toggle-group';
import { useAuth } from '../hooks/useAuth';
import { useDebouncedValue } from '../hooks/useDebouncedValue';
import { useDelayedLoading } from '../hooks/useDelayedLoading';
import { dayjs, toRfc3339 } from '../utils/format';
import {
  APPEND_SKELETON_COUNT,
  DEFAULT_GALLERY_PAGE_SIZE,
  STORAGE_KEYS,
  VIRTUAL_CHUNK_SIZE,
  VIRTUALIZE_THRESHOLD,
} from '../utils/constants';

function readStoredView(): GalleryView {
  try {
    return window.localStorage.getItem(STORAGE_KEYS.galleryView) === 'list' ? 'list' : 'masonry';
  } catch {
    return 'masonry';
  }
}

/** 分页边界可能重复返回同一条记录, 按 id 去重 (docs/webui.md 4.3) */
function dedupeById(pages: ImageRecord[][]): ImageRecord[] {
  const seen = new Set<number>();
  const result: ImageRecord[] = [];
  for (const page of pages) {
    for (const record of page) {
      if (seen.has(record.id)) continue;
      seen.add(record.id);
      result.push(record);
    }
  }
  return result;
}

function chunkRecords(records: ImageRecord[], size: number): ImageRecord[][] {
  const chunks: ImageRecord[][] = [];
  for (let index = 0; index < records.length; index += size) {
    chunks.push(records.slice(index, index + size));
  }
  return chunks;
}

export default function GalleryPage() {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const queryClient = useQueryClient();
  const [searchParams] = useSearchParams();

  const [filters, setFilters] = useState<GalleryFiltersValue>(() => ({
    keyword: '',
    format: searchParams.get('format') ?? '',
    from: searchParams.get('from') ? dayjs(searchParams.get('from')) : null,
    to: searchParams.get('to') ? dayjs(searchParams.get('to')) : null,
  }));
  const [view, setViewState] = useState<GalleryView>(readStoredView);
  const [paginationModel, setPaginationModel] = useState({
    page: 0,
    pageSize: DEFAULT_GALLERY_PAGE_SIZE,
  });
  const [selectionMode, setSelectionMode] = useState(false);
  const [selectedIds, setSelectedIds] = useState<number[]>([]);
  const [detailId, setDetailId] = useState<number | null>(null);
  const { hasPermission } = useAuth();
  const canDelete = hasPermission('delete_own') || hasPermission('delete_all');

  const debouncedKeyword = useDebouncedValue(filters.keyword, 400);

  const queryParams = useMemo(
    () => ({
      keyword: debouncedKeyword.trim() === '' ? undefined : debouncedKeyword.trim(),
      format: filters.format === '' ? undefined : filters.format,
      from: toRfc3339(filters.from),
      to: toRfc3339(filters.to, true),
    }),
    [debouncedKeyword, filters.format, filters.from, filters.to],
  );

  const infinite = useInfiniteQuery({
    queryKey: ['images', 'masonry', queryParams],
    // 不传 page_size: 由服务端按设置项 gallery_page_size 决定 (docs/webui.md 4.3)
    queryFn: ({ pageParam }) => imagesApi.list({ ...queryParams, page: pageParam }),
    initialPageParam: 1,
    getNextPageParam: (lastPage, pages) =>
      lastPage.items.length < lastPage.page_size ? undefined : pages.length + 1,
    enabled: view === 'masonry',
  });

  const listQuery = useQuery({
    queryKey: ['images', 'list', queryParams, paginationModel.page, paginationModel.pageSize],
    queryFn: () =>
      imagesApi.list({
        ...queryParams,
        page: paginationModel.page + 1,
        page_size: paginationModel.pageSize,
      }),
    enabled: view === 'list',
    placeholderData: keepPreviousData,
  });

  const items = useMemo(
    () => dedupeById((infinite.data?.pages ?? []).map((page) => page.items)),
    [infinite.data],
  );

  /** 记录是否已经成功加载过内容 (区分路由级首屏骨架与局部刷新骨架) */
  const loadedOnceRef = useRef(false);
  const contentCount = view === 'masonry' ? items.length : (listQuery.data?.items.length ?? 0);
  useEffect(() => {
    if (contentCount > 0) loadedOnceRef.current = true;
  }, [contentCount]);

  const pending = view === 'masonry' ? infinite.isPending : listQuery.isPending;
  const delayedLoading = useDelayedLoading(pending);
  const showSkeleton = pending && (!loadedOnceRef.current || delayedLoading);
  // 列表视图的筛选/翻页属于局部刷新: 延迟 200ms 显示骨架, 最短 300ms, 避免闪烁
  const listRefreshing = useDelayedLoading(
    view === 'list' && listQuery.isFetching && listQuery.isPlaceholderData && !showSkeleton,
  );

  // 筛选变化: 重置列表与滚动位置, 清理选择 (分页由 queryKey 变化重置)
  useEffect(() => {
    window.scrollTo({ top: 0 });
    setPaginationModel((model) => (model.page === 0 ? model : { ...model, page: 0 }));
    setSelectedIds([]);
  }, [queryParams]);

  const { ref: sentinelRef, inView } = useInView({ rootMargin: '400px' });
  const fetchNext = infinite.fetchNextPage;
  const hasNextPage = infinite.hasNextPage;
  const fetchingNext = infinite.isFetchingNextPage;

  useEffect(() => {
    if (view !== 'masonry' || items.length > VIRTUALIZE_THRESHOLD) return;
    if (inView && hasNextPage && !fetchingNext) void fetchNext();
  }, [view, inView, hasNextPage, fetchingNext, fetchNext, items.length]);

  // 详情大图: 按需加载原图并预加载相邻图片 (docs/webui.md 4.2)
  useEffect(() => {
    if (detailId === null) return;
    const index = items.findIndex((record) => record.id === detailId);
    if (index === -1) return;
    for (const neighbor of [items[index - 1], items[index + 1]]) {
      if (!neighbor) continue;
      const preload = new Image();
      preload.src = neighbor.url;
    }
  }, [detailId, items]);

  const batchDelete = useMutation({
    mutationFn: (ids: number[]) => imagesApi.batchDelete(ids),
    onSuccess: async (result) => {
      const retained = result.items.filter((item) => !item.object_deleted);
      await queryClient.invalidateQueries({ queryKey: ['images'] });
      setSelectedIds([]);
      toast.success(t('gallery.delete.successBatch', { count: result.items.length }));
      if (retained.length > 0) {
        const reasons = [...new Set(retained.map((item) => item.reason))];
        toast.warning(
          t('gallery.delete.objectRetained', {
            reason: reasons.map((reason) => t(`gallery.delete.reason.${reason}`)).join(', '),
          }),
        );
      }
    },
  });

  const records = useMemo(
    () => (view === 'masonry' ? items : (listQuery.data?.items ?? [])),
    [view, items, listQuery.data],
  );
  const selectedRecords = useMemo(
    () =>
      selectedIds
        .map((id) => records.find((record) => record.id === id))
        .filter((record): record is ImageRecord => Boolean(record)),
    [selectedIds, records],
  );

  const total =
    view === 'masonry' ? (infinite.data?.pages[0]?.total ?? 0) : (listQuery.data?.total ?? 0);

  const setView = (next: GalleryView) => {
    setViewState(next);
    setSelectedIds([]);
    setSelectionMode(false);
    try {
      window.localStorage.setItem(STORAGE_KEYS.galleryView, next);
    } catch {
      // 忽略存储失败
    }
  };

  const toggleSelected = useCallback((id: number) => {
    setSelectedIds((current) =>
      current.includes(id) ? current.filter((value) => value !== id) : [...current, id],
    );
  }, []);

  const handleBatchDelete = async () => {
    const ok = await confirm({
      title: t('gallery.selection.delete'),
      description: t('common.confirm.batchDelete', { count: selectedIds.length }),
      danger: true,
    });
    if (ok) batchDelete.mutate(selectedIds);
  };

  const renderCard = useCallback(
    (record: ImageRecord, layout: { columnWidth: number; height: number }) => (
      <GalleryImageCard
        record={record}
        height={layout.height}
        selectionMode={selectionMode}
        selected={selectedIds.includes(record.id)}
        onToggleSelect={toggleSelected}
        onOpen={(next) => setDetailId(next.id)}
      />
    ),
    [selectionMode, selectedIds, toggleSelected],
  );

  const appendedSkeletons = useMemo(
    () =>
      Array.from({ length: APPEND_SKELETON_COUNT }, (_, index) => (
        <SkeletonSurface
          key={`append-${index}`}
          label={t('gallery.more.appended')}
          className="h-full"
        >
          <Skeleton className="h-full w-full rounded-lg" />
        </SkeletonSurface>
      )),
    [t],
  );

  const virtualChunks = useMemo(() => chunkRecords(items, VIRTUAL_CHUNK_SIZE), [items]);

  const footer = useMemo(
    () => (
      <div className="py-3 text-center">
        {fetchingNext && (
          <p className="text-muted-foreground text-sm">{t('gallery.more.loading')}</p>
        )}
        {!hasNextPage && items.length > 0 && !fetchingNext && (
          <p className="text-muted-foreground text-sm">{t('gallery.more.end')}</p>
        )}
        {infinite.isError && items.length > 0 && (
          <Button
            size="sm"
            onClick={() => {
              void infinite.fetchNextPage();
            }}
          >
            {t('gallery.more.retry')}
          </Button>
        )}
      </div>
    ),
    [fetchingNext, hasNextPage, infinite, items.length, t],
  );

  const FooterComponent = useCallback(() => footer, [footer]);

  return (
    <div>
      <PageHeader
        title={t('gallery.title')}
        description={t('gallery.description')}
        actions={
          <>
            <ToggleGroup
              type="single"
              size="sm"
              value={view}
              onValueChange={(next) => {
                if (next) setView(next as GalleryView);
              }}
              aria-label={t('gallery.view.label')}
            >
              <ToggleGroupItem value="masonry" aria-label={t('gallery.view.masonry')}>
                <LayoutGrid className="size-4" />
              </ToggleGroupItem>
              <ToggleGroupItem value="list" aria-label={t('gallery.view.list')}>
                <List className="size-4" />
              </ToggleGroupItem>
            </ToggleGroup>
            <Toggle
              size="sm"
              pressed={selectionMode}
              onPressedChange={() => {
                setSelectionMode((value) => !value);
                setSelectedIds([]);
              }}
            >
              <Check className="size-4" />
              {selectionMode ? t('gallery.selection.exit') : t('gallery.selection.enter')}
            </Toggle>
            {total > 0 && (
              <Badge variant="outline">{t('common.table.total', { count: total })}</Badge>
            )}
          </>
        }
      />

      <GalleryFilters
        value={filters}
        onChange={(patch) => setFilters((current) => ({ ...current, ...patch }))}
        onClear={() => setFilters({ keyword: '', format: '', from: null, to: null })}
      />

      {showSkeleton && <MasonrySkeleton />}

      {!showSkeleton && view === 'list' && listRefreshing && <TableSkeleton rows={6} columns={7} />}

      {!showSkeleton && view === 'list' && !listRefreshing && (
        <GalleryListView
          rows={records}
          rowCount={total}
          loading={false}
          paginationModel={paginationModel}
          onPaginationModelChange={setPaginationModel}
          selectionMode={selectionMode}
          selection={selectedIds}
          onSelectionChange={setSelectedIds}
          onOpen={(record) => setDetailId(record.id)}
        />
      )}

      {!showSkeleton && view === 'list' && records.length === 0 && (
        <>
          {listQuery.isError ? (
            <ErrorState
              message={t('gallery.error.load')}
              onRetry={() => {
                void listQuery.refetch();
              }}
            />
          ) : (
            <EmptyState
              title={t('gallery.empty.title')}
              description={t('gallery.empty.description')}
            />
          )}
        </>
      )}

      {!showSkeleton && view === 'masonry' && items.length === 0 && (
        <>
          {infinite.isError ? (
            <ErrorState
              message={t('gallery.error.load')}
              onRetry={() => {
                void infinite.refetch();
              }}
            />
          ) : (
            <EmptyState
              title={t('gallery.empty.title')}
              description={t('gallery.empty.description')}
            />
          )}
        </>
      )}

      {!showSkeleton && view === 'masonry' && items.length > VIRTUALIZE_THRESHOLD && (
        <Virtuoso
          useWindowScroll
          data={virtualChunks}
          components={{ Footer: FooterComponent }}
          endReached={() => {
            if (hasNextPage && !fetchingNext) void fetchNext();
          }}
          itemContent={(_index, chunk) => (
            <div className="mb-3">
              <MasonryGrid
                items={chunk}
                getKey={(record) => record.id}
                getRatio={(record) => ({ width: record.width, height: record.height })}
                renderItem={renderCard}
              />
            </div>
          )}
        />
      )}

      {!showSkeleton &&
        view === 'masonry' &&
        items.length > 0 &&
        items.length <= VIRTUALIZE_THRESHOLD && (
          <>
            <MasonryGrid
              items={items}
              getKey={(record) => record.id}
              getRatio={(record) => ({ width: record.width, height: record.height })}
              renderItem={renderCard}
              extraCells={fetchingNext ? appendedSkeletons : []}
            />
            <div ref={sentinelRef} className="h-2" />
            {footer}
          </>
        )}

      <GalleryBatchBar
        records={selectedRecords}
        canDelete={canDelete}
        deleting={batchDelete.isPending}
        onClear={() => setSelectedIds([])}
        onExit={() => {
          setSelectionMode(false);
          setSelectedIds([]);
        }}
        onDelete={() => {
          void handleBatchDelete();
        }}
      />

      {batchDelete.isError && (
        <Alert variant="destructive" className="mt-4">
          <AlertDescription>{t('common.state.failed')}</AlertDescription>
        </Alert>
      )}

      <ImageDetailDrawer
        imageId={detailId}
        open={detailId !== null}
        onClose={() => setDetailId(null)}
      />
    </div>
  );
}
