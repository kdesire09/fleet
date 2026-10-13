// Subset of Fleet API types used by the POC.
// Source of truth: frontend/interfaces/*.ts in the legacy React app and
// docs/REST API/rest-api.md. These can be copied over as the port grows.

export type HostStatus = 'online' | 'offline' | 'new' | 'missing'

export interface FleetUser {
  id: number
  name: string
  email: string
  global_role: string | null
  gravatar_url?: string
  api_only: boolean
}

export interface LoginResponse {
  user: FleetUser
  token: string
}

export interface HostIssues {
  total_issues_count: number
  critical_vulnerabilities_count?: number
  failing_policies_count: number
}

export interface Host {
  id: number
  display_name: string
  hostname: string
  computer_name: string
  platform: string
  os_version: string
  osquery_version: string
  hardware_model: string
  primary_ip: string
  status: HostStatus
  seen_time: string
  team_id: number | null
  team_name: string | null
  issues: HostIssues
}

export interface HostsResponse {
  hosts: Host[]
}

export interface HostsCountResponse {
  count: number
}

export interface FleetErrorBody {
  message: string
  errors?: { name: string, reason: string }[]
}
