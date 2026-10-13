<script setup lang="ts">
import type { DropdownMenuItem, NavigationMenuItem } from '@nuxt/ui'

const { user, logout } = useAuth()

// Only "Hosts" is ported for now; other entries show the target navigation.
const items: NavigationMenuItem[] = [{
  label: 'Tableau de bord',
  icon: 'i-lucide-layout-dashboard',
  disabled: true,
}, {
  label: 'Hosts',
  icon: 'i-lucide-monitor',
  to: '/hosts',
}, {
  label: 'Contrôles',
  icon: 'i-lucide-shield-check',
  disabled: true,
}, {
  label: 'Logiciels',
  icon: 'i-lucide-package',
  disabled: true,
}, {
  label: 'Rapports',
  icon: 'i-lucide-file-chart-column',
  disabled: true,
}, {
  label: 'Policies',
  icon: 'i-lucide-list-checks',
  disabled: true,
}]

const userMenu = computed<DropdownMenuItem[][]>(() => [[{
  type: 'label',
  label: user.value?.email,
}], [{
  label: 'Se déconnecter',
  icon: 'i-lucide-log-out',
  onSelect: () => logout(),
}]])
</script>

<template>
  <UDashboardGroup>
    <UDashboardSidebar collapsible resizable :ui="{ footer: 'border-t border-default' }">
      <template #header="{ collapsed }">
        <div class="flex items-center gap-2" :class="collapsed && 'mx-auto'">
          <img src="/favicon.svg" alt="" class="size-6 shrink-0">
          <span v-if="!collapsed" class="font-semibold">Fleet</span>
        </div>
      </template>

      <template #default="{ collapsed }">
        <UNavigationMenu :collapsed="collapsed" :items="items" orientation="vertical" />
      </template>

      <template #footer="{ collapsed }">
        <UDropdownMenu :items="userMenu" :content="{ align: 'start', side: 'top' }" class="w-full">
          <UButton
            :avatar="{ src: user?.gravatar_url || undefined, alt: user?.name }"
            :label="collapsed ? undefined : user?.name"
            :trailing-icon="collapsed ? undefined : 'i-lucide-chevrons-up-down'"
            color="neutral"
            variant="ghost"
            block
            :square="collapsed"
          />
        </UDropdownMenu>
      </template>
    </UDashboardSidebar>

    <slot />
  </UDashboardGroup>
</template>
