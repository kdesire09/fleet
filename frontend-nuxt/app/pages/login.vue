<script setup lang="ts">
import type { AuthFormField, FormSubmitEvent } from '@nuxt/ui'
import * as z from 'zod'

definePageMeta({ layout: 'auth' })

const { login } = useAuth()
const route = useRoute()

const loading = ref(false)
const error = ref<string | null>(null)

const fields: AuthFormField[] = [{
  name: 'email',
  type: 'email',
  label: 'Email',
  placeholder: 'vous@entreprise.com',
  required: true,
}, {
  name: 'password',
  type: 'password',
  label: 'Mot de passe',
  placeholder: 'Votre mot de passe',
  required: true,
}]

const schema = z.object({
  email: z.email('Email invalide'),
  password: z.string('Mot de passe requis').min(1, 'Mot de passe requis'),
})

type Schema = z.output<typeof schema>

async function onSubmit(event: FormSubmitEvent<Schema>) {
  loading.value = true
  error.value = null
  try {
    await login(event.data.email, event.data.password)
    const redirect = typeof route.query.redirect === 'string' && route.query.redirect.startsWith('/')
      ? route.query.redirect
      : '/hosts'
    await navigateTo(redirect)
  }
  catch (err) {
    error.value = (err as Error).message === 'MFA_REQUIRED'
      ? 'Un email de vérification vous a été envoyé. La connexion MFA n’est pas encore gérée par ce prototype.'
      : fleetErrorMessage(err, 'Email ou mot de passe incorrect')
  }
  finally {
    loading.value = false
  }
}
</script>

<template>
  <UPageCard class="w-full max-w-md">
    <UAuthForm
      :schema="schema"
      :fields="fields"
      :loading="loading"
      title="Connexion"
      description="Connectez-vous à votre console Fleet."
      icon="i-lucide-lock"
      :submit="{ label: 'Se connecter' }"
      @submit="onSubmit"
    >
      <template #validation>
        <UAlert
          v-if="error"
          color="error"
          variant="subtle"
          icon="i-lucide-circle-alert"
          :title="error"
        />
      </template>
    </UAuthForm>
  </UPageCard>
</template>
