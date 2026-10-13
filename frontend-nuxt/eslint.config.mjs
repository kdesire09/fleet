import antfu from '@antfu/eslint-config'

export default antfu(
  {
    formatters: true,
    vue: true,
    typescript: true,

    stylistic: {
      indent: 2, // 4, or 'tab'
      quotes: 'single', // or 'double'
    },

    ignores: ['dist', 'node_modules', '.agent', '.agents'],
  },
  {
    rules: {
      'pnpm/yaml-enforce-settings': 'off',
    },
  },
)
