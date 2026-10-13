<script setup lang="ts">
import type { TableColumn } from '@nuxt/ui'
import type { SortingState } from '@tanstack/vue-table'
import type { Host, HostsCountResponse, HostsResponse, HostStatus } from '~/types/fleet'
import { refDebounced } from '@vueuse/core'
import { h, resolveComponent } from 'vue'

const UBadge = resolveComponent('UBadge')
const UButton = resolveComponent('UButton')
const UIcon = resolveComponent('UIcon')

const api = useFleetApi()
const toast = useToast()

const PER_PAGE = 20

// --- Filters / sorting / pagination (all server-side, like the legacy UI) ---
const search = ref('')
const debouncedSearch = refDebounced(search, 300)
const status = ref<HostStatus | 'all'>('all')
const sorting = ref<SortingState>([{ id: 'display_name', desc: false }])
const page = ref(1)

const statusItems = [
  { label: 'Tous les statuts', value: 'all' },
  ...Object.entries(HOST_STATUS).map(([value, { label }]) => ({ label, value })),
]

// Any filter change sends the user back to the first page.
watch([debouncedSearch, status, sorting], () => {
  page.value = 1
})

const filters = computed(() => ({
  query: debouncedSearch.value.trim() || undefined,
  status: status.value === 'all' ? undefined : status.value,
}))

const listQuery = computed(() => ({
  ...filters.value,
  page: page.value - 1,
  per_page: PER_PAGE,
  order_key: sorting.value[0]?.id ?? 'display_name',
  order_direction: sorting.value[0]?.desc ? 'desc' : 'asc',
}))

const { data: hostsData, status: hostsStatus, error, refresh } = await useAsyncData(
  'hosts',
  () => api<HostsResponse>('/hosts', { query: listQuery.value }),
  { watch: [listQuery] },
)

const { data: countData, refresh: refreshCount } = await useAsyncData(
  'hosts-count',
  () => api<HostsCountResponse>('/hosts/count', { query: filters.value }),
  { watch: [filters] },
)

const hosts = computed(() => hostsData.value?.hosts ?? [])
const total = computed(() => countData.value?.count ?? 0)
const loading = computed(() => hostsStatus.value === 'pending')

watch(error, (err) => {
  if (err) {
    toast.add({ title: 'Impossible de charger les hosts', description: fleetErrorMessage(err), color: 'error' })
  }
})

function refreshAll() {
  refresh()
  refreshCount()
}

// --- Columns ---
function sortableHeader(label: string) {
  return ({ column }: { column: { getIsSorted: () => false | 'asc' | 'desc', toggleSorting: (desc?: boolean) => void } }) => {
    const sorted = column.getIsSorted()
    return h(UButton, {
      color: 'neutral',
      variant: 'ghost',
      label,
      icon: sorted ? (sorted === 'asc' ? 'i-lucide-arrow-up-narrow-wide' : 'i-lucide-arrow-down-wide-narrow') : 'i-lucide-arrow-up-down',
      class: '-mx-2.5',
      onClick: () => column.toggleSorting(sorted === 'asc'),
    })
  }
}

const columns: TableColumn<Host>[] = [{
  accessorKey: 'display_name',
  header: sortableHeader('Host'),
  cell: ({ row }) => h('div', { class: 'flex items-center gap-2 min-w-0' }, [
    h(UIcon, { name: platformIcon(row.original.platform), class: 'size-4 shrink-0 text-muted' }),
    h('span', { class: 'font-medium text-highlighted truncate' }, row.original.display_name),
  ]),
}, {
  accessorKey: 'status',
  header: 'Statut',
  enableSorting: false,
  cell: ({ row }) => {
    const s = HOST_STATUS[row.original.status] ?? HOST_STATUS.offline
    return h(UBadge, { color: s.color, variant: 'subtle' }, () => s.label)
  },
}, {
  accessorKey: 'issues',
  header: sortableHeader('Problèmes'),
  cell: ({ row }) => {
    const count = row.original.issues?.total_issues_count ?? 0
    return count > 0
      ? h(UBadge, { color: 'error', variant: 'soft', icon: 'i-lucide-triangle-alert' }, () => String(count))
      : h('span', { class: 'text-dimmed' }, '—')
  },
}, {
  accessorKey: 'team_name',
  header: sortableHeader('Fleet'),
  cell: ({ row }) => row.original.team_name ?? h('span', { class: 'text-dimmed' }, 'Aucune'),
}, {
  accessorKey: 'os_version',
  header: sortableHeader('Système'),
  cell: ({ row }) => row.original.os_version || '—',
}, {
  accessorKey: 'osquery_version',
  header: 'osquery',
  enableSorting: false,
  cell: ({ row }) => row.original.osquery_version || '—',
}, {
  accessorKey: 'primary_ip',
  header: sortableHeader('IP'),
  cell: ({ row }) => h('span', { class: 'font-mono text-xs' }, row.original.primary_ip || '—'),
}, {
  accessorKey: 'seen_time',
  header: sortableHeader('Vu'),
  cell: ({ row }) => h('span', { title: row.original.seen_time }, timeAgo(row.original.seen_time)),
}]
</script>

<template>
  <UDashboardPanel id="hosts">
    <template #header>
      <UDashboardNavbar title="Hosts">
        <template #leading>
          <UDashboardSidebarCollapse />
        </template>

        <template #trailing>
          <UBadge :label="total" variant="subtle" />
        </template>

        <template #right>
          <UButton
            icon="i-lucide-refresh-cw"
            color="neutral"
            variant="ghost"
            aria-label="Rafraîchir"
            :loading="loading"
            @click="refreshAll"
          />
        </template>
      </UDashboardNavbar>

      <UDashboardToolbar>
        <template #left>
          <UInput
            v-model="search"
            icon="i-lucide-search"
            placeholder="Nom, hostname, UUID, numéro de série ou IP"
            class="w-full sm:w-96"
          />
        </template>

        <template #right>
          <USelect v-model="status" :items="statusItems" class="w-44" />
        </template>
      </UDashboardToolbar>
    </template>

    <template #body>
      <UTable
        v-model:sorting="sorting"
        :sorting-options="{ manualSorting: true, enableSortingRemoval: false }"
        :data="hosts"
        :columns="columns"
        :loading="loading"
        :get-row-id="(row: Host) => String(row.id)"
        empty="Aucun host ne correspond à ces filtres."
        sticky
        class="shrink-0"
        :ui="{
          base: 'table-fixed border-separate border-spacing-0',
          thead: '[&>tr]:bg-elevated/50 [&>tr]:after:content-none',
          tbody: '[&>tr]:last:[&>td]:border-b-0',
          th: 'py-2 first:rounded-l-lg last:rounded-r-lg border-y border-default first:border-l last:border-r',
          td: 'border-b border-default',
        }"
      />

      <div class="flex items-center justify-between gap-3 border-t border-default pt-4 mt-auto">
        <p class="text-sm text-muted">
          <template v-if="total > 0">
            {{ (page - 1) * PER_PAGE + 1 }}–{{ Math.min(page * PER_PAGE, total) }} sur {{ total }} hosts
          </template>
          <template v-else>
            Aucun host
          </template>
        </p>

        <UPagination
          v-model:page="page"
          :items-per-page="PER_PAGE"
          :total="total"
        />
      </div>
    </template>
  </UDashboardPanel>
</template>
