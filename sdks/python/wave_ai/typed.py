"""Generated typed endpoints over the shared transport. Do not edit."""
from __future__ import annotations

from typing import Optional

from wave_ai_generated.models.agents_agent import AgentsAgent
from wave_ai_generated.models.agents_config import AgentsConfig
from wave_ai_generated.models.agents_list_response import AgentsListResponse
from wave_ai_generated.models.agents_update_request import AgentsUpdateRequest
from wave_ai_generated.models.agents_versions_response import AgentsVersionsResponse
from wave_ai_generated.models.console_list import ConsoleList
from wave_ai_generated.models.console_record import ConsoleRecord
from wave_ai_generated.models.deployments_create_request import DeploymentsCreateRequest
from wave_ai_generated.models.deployments_deployment import DeploymentsDeployment
from wave_ai_generated.models.deployments_list_response import DeploymentsListResponse
from wave_ai_generated.models.deployments_pause_request import DeploymentsPauseRequest
from wave_ai_generated.models.deployments_run import DeploymentsRun
from wave_ai_generated.models.deployments_runs_response import DeploymentsRunsResponse
from wave_ai_generated.models.deployments_schedule_response import DeploymentsScheduleResponse
from wave_ai_generated.models.deployments_update_request import DeploymentsUpdateRequest
from wave_ai_generated.models.deployments_versions_response import DeploymentsVersionsResponse
from wave_ai_generated.models.environments_create_request import EnvironmentsCreateRequest
from wave_ai_generated.models.environments_environment import EnvironmentsEnvironment
from wave_ai_generated.models.environments_list_response import EnvironmentsListResponse
from wave_ai_generated.models.execution_create_session_request import ExecutionCreateSessionRequest
from wave_ai_generated.models.execution_event import ExecutionEvent
from wave_ai_generated.models.execution_events_response import ExecutionEventsResponse
from wave_ai_generated.models.execution_generations_response import ExecutionGenerationsResponse
from wave_ai_generated.models.execution_inputs_response import ExecutionInputsResponse
from wave_ai_generated.models.execution_messages_response import ExecutionMessagesResponse
from wave_ai_generated.models.execution_reconcile_request import ExecutionReconcileRequest
from wave_ai_generated.models.execution_required_actions_response import ExecutionRequiredActionsResponse
from wave_ai_generated.models.execution_session import ExecutionSession
from wave_ai_generated.models.execution_sessions_response import ExecutionSessionsResponse
from wave_ai_generated.models.execution_task import ExecutionTask
from wave_ai_generated.models.execution_task_request import ExecutionTaskRequest
from wave_ai_generated.models.execution_tasks_response import ExecutionTasksResponse
from wave_ai_generated.models.execution_text_request import ExecutionTextRequest
from wave_ai_generated.models.execution_tool_calls_response import ExecutionToolCallsResponse
from wave_ai_generated.models.execution_tool_result_request import ExecutionToolResultRequest
from wave_ai_generated.models.execution_trace import ExecutionTrace
from wave_ai_generated.models.files_file import FilesFile
from wave_ai_generated.models.files_list_response import FilesListResponse
from wave_ai_generated.models.memory_conflicts_response import MemoryConflictsResponse
from wave_ai_generated.models.memory_create_store_request import MemoryCreateStoreRequest
from wave_ai_generated.models.memory_delete_entry_request import MemoryDeleteEntryRequest
from wave_ai_generated.models.memory_entries_response import MemoryEntriesResponse
from wave_ai_generated.models.memory_entry import MemoryEntry
from wave_ai_generated.models.memory_list_response import MemoryListResponse
from wave_ai_generated.models.memory_revisions_response import MemoryRevisionsResponse
from wave_ai_generated.models.memory_store import MemoryStore
from wave_ai_generated.models.memory_write_entry_request import MemoryWriteEntryRequest
from wave_ai_generated.models.observe_log import ObserveLog
from wave_ai_generated.models.skills_list_response import SkillsListResponse
from wave_ai_generated.models.telemetry_metrics import TelemetryMetrics
from wave_ai_generated.models.vault_create_request import VaultCreateRequest
from wave_ai_generated.models.vault_credential import VaultCredential
from wave_ai_generated.models.vault_list_response import VaultListResponse
from wave_ai_generated.models.vault_rotate_request import VaultRotateRequest
from wave_ai_generated.models.vault_validate_request import VaultValidateRequest
from wave_ai_generated.models.vault_validation import VaultValidation
from wave_ai_generated.models.vault_vault import VaultVault
from wave_ai_generated.models.vault_vault_request import VaultVaultRequest
from wave_ai_generated.models.vault_vaults_response import VaultVaultsResponse
from wave_ai_generated.models.webhooks_create_request import WebhooksCreateRequest
from wave_ai_generated.models.webhooks_deliveries_response import WebhooksDeliveriesResponse
from wave_ai_generated.models.webhooks_delivery import WebhooksDelivery
from wave_ai_generated.models.webhooks_list_response import WebhooksListResponse
from wave_ai_generated.models.webhooks_subscription import WebhooksSubscription
from wave_ai_generated.models.webhooks_update_request import WebhooksUpdateRequest

from .client import AsyncClient as _AsyncBase
from .client import Client as _SyncBase
from .client import Response


class Client(_SyncBase):
    def health(self) -> Response:
        """Health check."""
        return self.call('health')

    def agents_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> AgentsListResponse:
        """List Agents."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('agentsList', query=query)
        return AgentsListResponse.from_dict(response.data)

    def agents_create(self, *, body: AgentsConfig) -> AgentsAgent:
        """Create an Agent."""
        response = self.call('agentsCreate', body=body.to_dict())
        return AgentsAgent.from_dict(response.data)

    def agents_get(self, id: str) -> AgentsAgent:
        """Get an Agent."""
        response = self.call('agentsGet', path={'id': id})
        return AgentsAgent.from_dict(response.data)

    def agents_update(self, id: str, *, body: AgentsUpdateRequest) -> AgentsAgent:
        """Update an Agent."""
        response = self.call('agentsUpdate', path={'id': id}, body=body.to_dict())
        return AgentsAgent.from_dict(response.data)

    def agents_archive(self, id: str) -> Response:
        """Archive an Agent."""
        return self.call('agentsArchive', path={'id': id})

    def agents_versions(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> AgentsVersionsResponse:
        """Agent version history."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('agentsVersions', path={'id': id}, query=query)
        return AgentsVersionsResponse.from_dict(response.data)

    def console_event(self, session: str, sequence: int) -> ExecutionEvent:
        """Read an execution event."""
        response = self.call('consoleEvent', path={'session': session, 'sequence': sequence})
        return ExecutionEvent.from_dict(response.data)

    def console_log(self, id: str) -> ObserveLog:
        """Read a structured log."""
        response = self.call('consoleLog', path={'id': id})
        return ObserveLog.from_dict(response.data)

    def console_metrics(self, *, from_: Optional[str] = None, to: Optional[str] = None) -> TelemetryMetrics:
        """Query monitoring metrics."""
        query = {key: value for key, value in {'from': from_, 'to': to}.items() if value is not None}
        response = self.call('consoleMetrics', query=query)
        return TelemetryMetrics.from_dict(response.data)

    def console_browse(self, kind: str, *, q: Optional[str] = None, state: Optional[str] = None, module: Optional[str] = None, trace_id: Optional[str] = None, span_id: Optional[str] = None, time_field: Optional[str] = None, session_id: Optional[str] = None, agent_id: Optional[str] = None, environment_id: Optional[str] = None, skill_id: Optional[str] = None, task_id: Optional[str] = None, before: Optional[str] = None, after: Optional[str] = None, cursor: Optional[str] = None, limit: Optional[int] = None) -> ConsoleList:
        """Browse console resources."""
        query = {key: value for key, value in {'q': q, 'state': state, 'module': module, 'trace_id': trace_id, 'span_id': span_id, 'time_field': time_field, 'session_id': session_id, 'agent_id': agent_id, 'environment_id': environment_id, 'skill_id': skill_id, 'task_id': task_id, 'before': before, 'after': after, 'cursor': cursor, 'limit': limit}.items() if value is not None}
        response = self.call('consoleBrowse', path={'kind': kind}, query=query)
        return ConsoleList.from_dict(response.data)

    def console_resource(self, kind: str, id: str) -> ConsoleRecord:
        """Read a console resource."""
        response = self.call('consoleResource', path={'kind': kind, 'id': id})
        return ConsoleRecord.from_dict(response.data)

    def vault_list(self, *, limit: Optional[int] = None, vault_id: Optional[str] = None, offset: Optional[int] = None) -> VaultListResponse:
        """List credentials."""
        query = {key: value for key, value in {'limit': limit, 'vault_id': vault_id, 'offset': offset}.items() if value is not None}
        response = self.call('vaultList', query=query)
        return VaultListResponse.from_dict(response.data)

    def vault_create(self, *, body: VaultCreateRequest) -> VaultCredential:
        """Store a credential."""
        response = self.call('vaultCreate', body=body.to_dict())
        return VaultCredential.from_dict(response.data)

    def vault_revoke(self, id: str) -> Response:
        """Revoke a credential."""
        return self.call('vaultRevoke', path={'id': id})

    def vault_rotate(self, id: str, *, body: VaultRotateRequest) -> VaultCredential:
        """Rotate a credential."""
        response = self.call('vaultRotate', path={'id': id}, body=body.to_dict())
        return VaultCredential.from_dict(response.data)

    def vault_validate(self, id: str, *, body: VaultValidateRequest) -> VaultValidation:
        """Validate credential binding and encryption."""
        response = self.call('vaultValidate', path={'id': id}, body=body.to_dict())
        return VaultValidation.from_dict(response.data)

    def deployments_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> DeploymentsListResponse:
        """List deployments."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('deploymentsList', query=query)
        return DeploymentsListResponse.from_dict(response.data)

    def deployments_create(self, *, body: DeploymentsCreateRequest) -> DeploymentsDeployment:
        """Create a deployment."""
        response = self.call('deploymentsCreate', body=body.to_dict())
        return DeploymentsDeployment.from_dict(response.data)

    def deployments_get(self, id: str) -> DeploymentsDeployment:
        """Get a deployment."""
        response = self.call('deploymentsGet', path={'id': id})
        return DeploymentsDeployment.from_dict(response.data)

    def deployments_pause(self, id: str, *, body: DeploymentsPauseRequest) -> DeploymentsDeployment:
        """Pause or resume a deployment."""
        response = self.call('deploymentsPause', path={'id': id}, body=body.to_dict())
        return DeploymentsDeployment.from_dict(response.data)

    def deployments_update(self, id: str, *, body: DeploymentsUpdateRequest) -> DeploymentsDeployment:
        """Update deployment configuration."""
        response = self.call('deploymentsUpdate', path={'id': id}, body=body.to_dict())
        return DeploymentsDeployment.from_dict(response.data)

    def deployments_run(self, id: str, *, idempotency_key: Optional[str] = None) -> DeploymentsRun:
        """Trigger a deployment manually."""
        headers = {key: value for key, value in {'Idempotency-Key': idempotency_key}.items() if value is not None}
        response = self.call('deploymentsRun', path={'id': id}, headers=headers)
        return DeploymentsRun.from_dict(response.data)

    def deployments_runs(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> DeploymentsRunsResponse:
        """Deployment run records."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('deploymentsRuns', path={'id': id}, query=query)
        return DeploymentsRunsResponse.from_dict(response.data)

    def deployments_schedule(self, id: str) -> DeploymentsScheduleResponse:
        """Upcoming occurrences."""
        response = self.call('deploymentsSchedule', path={'id': id})
        return DeploymentsScheduleResponse.from_dict(response.data)

    def deployments_versions(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> DeploymentsVersionsResponse:
        """Deployment versions."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('deploymentsVersions', path={'id': id}, query=query)
        return DeploymentsVersionsResponse.from_dict(response.data)

    def environments_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> EnvironmentsListResponse:
        """List environments."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('environmentsList', query=query)
        return EnvironmentsListResponse.from_dict(response.data)

    def environments_create(self, *, body: EnvironmentsCreateRequest) -> EnvironmentsEnvironment:
        """Create an environment."""
        response = self.call('environmentsCreate', body=body.to_dict())
        return EnvironmentsEnvironment.from_dict(response.data)

    def environments_get(self, id: str) -> EnvironmentsEnvironment:
        """Get an environment."""
        response = self.call('environmentsGet', path={'id': id})
        return EnvironmentsEnvironment.from_dict(response.data)

    def environments_archive(self, id: str) -> Response:
        """Archive an environment."""
        return self.call('environmentsArchive', path={'id': id})

    def files_list(self, *, session_id: Optional[str] = None, task_id: Optional[str] = None, limit: Optional[int] = None, offset: Optional[int] = None) -> FilesListResponse:
        """List files."""
        query = {key: value for key, value in {'session_id': session_id, 'task_id': task_id, 'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('filesList', query=query)
        return FilesListResponse.from_dict(response.data)

    def files_delete(self, id: str) -> Response:
        """Delete a file."""
        return self.call('filesDelete', path={'id': id})

    def files_get(self, id: str) -> FilesFile:
        """Get file metadata."""
        response = self.call('filesGet', path={'id': id})
        return FilesFile.from_dict(response.data)

    def memory_list_stores(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> MemoryListResponse:
        """List memory stores."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('memoryListStores', query=query)
        return MemoryListResponse.from_dict(response.data)

    def memory_create_store(self, *, body: MemoryCreateStoreRequest) -> MemoryStore:
        """Create a memory store."""
        response = self.call('memoryCreateStore', body=body.to_dict())
        return MemoryStore.from_dict(response.data)

    def memory_list_conflicts(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> MemoryConflictsResponse:
        """List memory conflicts."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('memoryListConflicts', path={'id': id}, query=query)
        return MemoryConflictsResponse.from_dict(response.data)

    def memory_delete_entry(self, id: str, *, body: MemoryDeleteEntryRequest) -> MemoryEntry:
        """Delete a memory entry."""
        response = self.call('memoryDeleteEntry', path={'id': id}, body=body.to_dict())
        return MemoryEntry.from_dict(response.data)

    def memory_list_entries(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> MemoryEntriesResponse:
        """List memory entries."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('memoryListEntries', path={'id': id}, query=query)
        return MemoryEntriesResponse.from_dict(response.data)

    def memory_write_entry(self, id: str, *, body: MemoryWriteEntryRequest) -> MemoryEntry:
        """Write a memory entry."""
        response = self.call('memoryWriteEntry', path={'id': id}, body=body.to_dict())
        return MemoryEntry.from_dict(response.data)

    def memory_list_revisions(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> MemoryRevisionsResponse:
        """Memory revision history."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('memoryListRevisions', path={'id': id}, query=query)
        return MemoryRevisionsResponse.from_dict(response.data)

    def execution_list_sessions(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionSessionsResponse:
        """List sessions."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('executionListSessions', query=query)
        return ExecutionSessionsResponse.from_dict(response.data)

    def execution_create_session(self, *, body: ExecutionCreateSessionRequest) -> ExecutionSession:
        """Create a session."""
        response = self.call('executionCreateSession', body=body.to_dict())
        return ExecutionSession.from_dict(response.data)

    def execution_get_session(self, id: str) -> ExecutionSession:
        """Get a session."""
        response = self.call('executionGetSession', path={'id': id})
        return ExecutionSession.from_dict(response.data)

    def execution_archive_session(self, id: str) -> Response:
        """Archive a session."""
        return self.call('executionArchiveSession', path={'id': id})

    def execution_list_events(self, id: str, *, after: Optional[int] = None, last_event_id: Optional[str] = None) -> ExecutionEventsResponse:
        """List session events."""
        query = {key: value for key, value in {'after': after}.items() if value is not None}
        headers = {key: value for key, value in {'Last-Event-ID': last_event_id}.items() if value is not None}
        response = self.call('executionListEvents', path={'id': id}, query=query, headers=headers)
        return ExecutionEventsResponse.from_dict(response.data)

    def execution_messages(self, id: str, *, after: Optional[int] = None, task_id: Optional[str] = None, last_event_id: Optional[str] = None) -> ExecutionMessagesResponse:
        """Session message history."""
        query = {key: value for key, value in {'after': after, 'task_id': task_id}.items() if value is not None}
        headers = {key: value for key, value in {'Last-Event-ID': last_event_id}.items() if value is not None}
        response = self.call('executionMessages', path={'id': id}, query=query, headers=headers)
        return ExecutionMessagesResponse.from_dict(response.data)

    def execution_required_actions(self, id: str) -> ExecutionRequiredActionsResponse:
        """Required user actions."""
        response = self.call('executionRequiredActions', path={'id': id})
        return ExecutionRequiredActionsResponse.from_dict(response.data)

    def execution_list_tasks(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionTasksResponse:
        """List session tasks."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('executionListTasks', path={'id': id}, query=query)
        return ExecutionTasksResponse.from_dict(response.data)

    def execution_create_task(self, id: str, *, body: ExecutionTaskRequest, idempotency_key: Optional[str] = None) -> ExecutionTask:
        """Create a task."""
        headers = {key: value for key, value in {'Idempotency-Key': idempotency_key}.items() if value is not None}
        response = self.call('executionCreateTask', path={'id': id}, headers=headers, body=body.to_dict())
        return ExecutionTask.from_dict(response.data)

    def skills_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> SkillsListResponse:
        """List skill packages."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('skillsList', query=query)
        return SkillsListResponse.from_dict(response.data)

    def execution_get_task(self, id: str) -> ExecutionTask:
        """Get a task."""
        response = self.call('executionGetTask', path={'id': id})
        return ExecutionTask.from_dict(response.data)

    def execution_delegate_task(self, id: str, *, body: ExecutionTaskRequest, idempotency_key: Optional[str] = None) -> ExecutionTask:
        """Delegate a child task."""
        headers = {key: value for key, value in {'Idempotency-Key': idempotency_key}.items() if value is not None}
        response = self.call('executionDelegateTask', path={'id': id}, headers=headers, body=body.to_dict())
        return ExecutionTask.from_dict(response.data)

    def execution_cancel_task(self, id: str) -> Response:
        """Cancel a task."""
        return self.call('executionCancelTask', path={'id': id})

    def execution_collaboration(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionTasksResponse:
        """Collaboration tasks."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('executionCollaboration', path={'id': id}, query=query)
        return ExecutionTasksResponse.from_dict(response.data)

    def execution_generations(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionGenerationsResponse:
        """Task model-call records."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('executionGenerations', path={'id': id}, query=query)
        return ExecutionGenerationsResponse.from_dict(response.data)

    def execution_list_inputs(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionInputsResponse:
        """List task inputs."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('executionListInputs', path={'id': id}, query=query)
        return ExecutionInputsResponse.from_dict(response.data)

    def execution_add_input(self, id: str, *, body: ExecutionTextRequest, idempotency_key: Optional[str] = None) -> Response:
        """Steer a task with additional input."""
        headers = {key: value for key, value in {'Idempotency-Key': idempotency_key}.items() if value is not None}
        return self.call('executionAddInput', path={'id': id}, headers=headers, body=body.to_dict())

    def execution_reconcile(self, id: str, *, body: ExecutionReconcileRequest) -> Response:
        """Reconcile a task."""
        return self.call('executionReconcile', path={'id': id}, body=body.to_dict())

    def execution_resume_task(self, id: str, *, body: ExecutionTextRequest) -> Response:
        """Resume a terminal child task."""
        return self.call('executionResumeTask', path={'id': id}, body=body.to_dict())

    def execution_list_tools(self, id: str) -> ExecutionToolCallsResponse:
        """List task tool calls."""
        response = self.call('executionListTools', path={'id': id})
        return ExecutionToolCallsResponse.from_dict(response.data)

    def execution_resolve_tool_result(self, id: str, call: str, *, body: ExecutionToolResultRequest) -> Response:
        """Submit tool approval or result."""
        return self.call('executionResolveToolResult', path={'id': id, 'call': call}, body=body.to_dict())

    def execution_trace(self, id: str) -> ExecutionTrace:
        """Task trace and timing breakdown."""
        response = self.call('executionTrace', path={'id': id})
        return ExecutionTrace.from_dict(response.data)

    def vaults_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> VaultVaultsResponse:
        """List vaults."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('vaultsList', query=query)
        return VaultVaultsResponse.from_dict(response.data)

    def vaults_create(self, *, body: VaultVaultRequest) -> VaultVault:
        """Create a vault."""
        response = self.call('vaultsCreate', body=body.to_dict())
        return VaultVault.from_dict(response.data)

    def vaults_archive(self, id: str) -> VaultVault:
        """Archive a vault."""
        response = self.call('vaultsArchive', path={'id': id})
        return VaultVault.from_dict(response.data)

    def vaults_get(self, id: str) -> VaultVault:
        """Get a vault."""
        response = self.call('vaultsGet', path={'id': id})
        return VaultVault.from_dict(response.data)

    def webhooks_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> WebhooksListResponse:
        """List webhooks."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('webhooksList', query=query)
        return WebhooksListResponse.from_dict(response.data)

    def webhooks_create(self, *, body: WebhooksCreateRequest) -> WebhooksSubscription:
        """Create a webhook."""
        response = self.call('webhooksCreate', body=body.to_dict())
        return WebhooksSubscription.from_dict(response.data)

    def webhooks_get(self, id: str) -> WebhooksSubscription:
        """Get a webhook."""
        response = self.call('webhooksGet', path={'id': id})
        return WebhooksSubscription.from_dict(response.data)

    def webhooks_update(self, id: str, *, body: WebhooksUpdateRequest) -> WebhooksSubscription:
        """Pause or resume a webhook."""
        response = self.call('webhooksUpdate', path={'id': id}, body=body.to_dict())
        return WebhooksSubscription.from_dict(response.data)

    def webhooks_deliveries(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> WebhooksDeliveriesResponse:
        """Webhook deliveries."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = self.call('webhooksDeliveries', path={'id': id}, query=query)
        return WebhooksDeliveriesResponse.from_dict(response.data)

    def webhooks_retry(self, id: str, delivery: str) -> WebhooksDelivery:
        """Retry delivery."""
        response = self.call('webhooksRetry', path={'id': id, 'delivery': delivery})
        return WebhooksDelivery.from_dict(response.data)


class AsyncClient(_AsyncBase):
    async def health(self) -> Response:
        """Health check."""
        return await self.call('health')

    async def agents_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> AgentsListResponse:
        """List Agents."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('agentsList', query=query)
        return AgentsListResponse.from_dict(response.data)

    async def agents_create(self, *, body: AgentsConfig) -> AgentsAgent:
        """Create an Agent."""
        response = await self.call('agentsCreate', body=body.to_dict())
        return AgentsAgent.from_dict(response.data)

    async def agents_get(self, id: str) -> AgentsAgent:
        """Get an Agent."""
        response = await self.call('agentsGet', path={'id': id})
        return AgentsAgent.from_dict(response.data)

    async def agents_update(self, id: str, *, body: AgentsUpdateRequest) -> AgentsAgent:
        """Update an Agent."""
        response = await self.call('agentsUpdate', path={'id': id}, body=body.to_dict())
        return AgentsAgent.from_dict(response.data)

    async def agents_archive(self, id: str) -> Response:
        """Archive an Agent."""
        return await self.call('agentsArchive', path={'id': id})

    async def agents_versions(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> AgentsVersionsResponse:
        """Agent version history."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('agentsVersions', path={'id': id}, query=query)
        return AgentsVersionsResponse.from_dict(response.data)

    async def console_event(self, session: str, sequence: int) -> ExecutionEvent:
        """Read an execution event."""
        response = await self.call('consoleEvent', path={'session': session, 'sequence': sequence})
        return ExecutionEvent.from_dict(response.data)

    async def console_log(self, id: str) -> ObserveLog:
        """Read a structured log."""
        response = await self.call('consoleLog', path={'id': id})
        return ObserveLog.from_dict(response.data)

    async def console_metrics(self, *, from_: Optional[str] = None, to: Optional[str] = None) -> TelemetryMetrics:
        """Query monitoring metrics."""
        query = {key: value for key, value in {'from': from_, 'to': to}.items() if value is not None}
        response = await self.call('consoleMetrics', query=query)
        return TelemetryMetrics.from_dict(response.data)

    async def console_browse(self, kind: str, *, q: Optional[str] = None, state: Optional[str] = None, module: Optional[str] = None, trace_id: Optional[str] = None, span_id: Optional[str] = None, time_field: Optional[str] = None, session_id: Optional[str] = None, agent_id: Optional[str] = None, environment_id: Optional[str] = None, skill_id: Optional[str] = None, task_id: Optional[str] = None, before: Optional[str] = None, after: Optional[str] = None, cursor: Optional[str] = None, limit: Optional[int] = None) -> ConsoleList:
        """Browse console resources."""
        query = {key: value for key, value in {'q': q, 'state': state, 'module': module, 'trace_id': trace_id, 'span_id': span_id, 'time_field': time_field, 'session_id': session_id, 'agent_id': agent_id, 'environment_id': environment_id, 'skill_id': skill_id, 'task_id': task_id, 'before': before, 'after': after, 'cursor': cursor, 'limit': limit}.items() if value is not None}
        response = await self.call('consoleBrowse', path={'kind': kind}, query=query)
        return ConsoleList.from_dict(response.data)

    async def console_resource(self, kind: str, id: str) -> ConsoleRecord:
        """Read a console resource."""
        response = await self.call('consoleResource', path={'kind': kind, 'id': id})
        return ConsoleRecord.from_dict(response.data)

    async def vault_list(self, *, limit: Optional[int] = None, vault_id: Optional[str] = None, offset: Optional[int] = None) -> VaultListResponse:
        """List credentials."""
        query = {key: value for key, value in {'limit': limit, 'vault_id': vault_id, 'offset': offset}.items() if value is not None}
        response = await self.call('vaultList', query=query)
        return VaultListResponse.from_dict(response.data)

    async def vault_create(self, *, body: VaultCreateRequest) -> VaultCredential:
        """Store a credential."""
        response = await self.call('vaultCreate', body=body.to_dict())
        return VaultCredential.from_dict(response.data)

    async def vault_revoke(self, id: str) -> Response:
        """Revoke a credential."""
        return await self.call('vaultRevoke', path={'id': id})

    async def vault_rotate(self, id: str, *, body: VaultRotateRequest) -> VaultCredential:
        """Rotate a credential."""
        response = await self.call('vaultRotate', path={'id': id}, body=body.to_dict())
        return VaultCredential.from_dict(response.data)

    async def vault_validate(self, id: str, *, body: VaultValidateRequest) -> VaultValidation:
        """Validate credential binding and encryption."""
        response = await self.call('vaultValidate', path={'id': id}, body=body.to_dict())
        return VaultValidation.from_dict(response.data)

    async def deployments_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> DeploymentsListResponse:
        """List deployments."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('deploymentsList', query=query)
        return DeploymentsListResponse.from_dict(response.data)

    async def deployments_create(self, *, body: DeploymentsCreateRequest) -> DeploymentsDeployment:
        """Create a deployment."""
        response = await self.call('deploymentsCreate', body=body.to_dict())
        return DeploymentsDeployment.from_dict(response.data)

    async def deployments_get(self, id: str) -> DeploymentsDeployment:
        """Get a deployment."""
        response = await self.call('deploymentsGet', path={'id': id})
        return DeploymentsDeployment.from_dict(response.data)

    async def deployments_pause(self, id: str, *, body: DeploymentsPauseRequest) -> DeploymentsDeployment:
        """Pause or resume a deployment."""
        response = await self.call('deploymentsPause', path={'id': id}, body=body.to_dict())
        return DeploymentsDeployment.from_dict(response.data)

    async def deployments_update(self, id: str, *, body: DeploymentsUpdateRequest) -> DeploymentsDeployment:
        """Update deployment configuration."""
        response = await self.call('deploymentsUpdate', path={'id': id}, body=body.to_dict())
        return DeploymentsDeployment.from_dict(response.data)

    async def deployments_run(self, id: str, *, idempotency_key: Optional[str] = None) -> DeploymentsRun:
        """Trigger a deployment manually."""
        headers = {key: value for key, value in {'Idempotency-Key': idempotency_key}.items() if value is not None}
        response = await self.call('deploymentsRun', path={'id': id}, headers=headers)
        return DeploymentsRun.from_dict(response.data)

    async def deployments_runs(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> DeploymentsRunsResponse:
        """Deployment run records."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('deploymentsRuns', path={'id': id}, query=query)
        return DeploymentsRunsResponse.from_dict(response.data)

    async def deployments_schedule(self, id: str) -> DeploymentsScheduleResponse:
        """Upcoming occurrences."""
        response = await self.call('deploymentsSchedule', path={'id': id})
        return DeploymentsScheduleResponse.from_dict(response.data)

    async def deployments_versions(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> DeploymentsVersionsResponse:
        """Deployment versions."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('deploymentsVersions', path={'id': id}, query=query)
        return DeploymentsVersionsResponse.from_dict(response.data)

    async def environments_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> EnvironmentsListResponse:
        """List environments."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('environmentsList', query=query)
        return EnvironmentsListResponse.from_dict(response.data)

    async def environments_create(self, *, body: EnvironmentsCreateRequest) -> EnvironmentsEnvironment:
        """Create an environment."""
        response = await self.call('environmentsCreate', body=body.to_dict())
        return EnvironmentsEnvironment.from_dict(response.data)

    async def environments_get(self, id: str) -> EnvironmentsEnvironment:
        """Get an environment."""
        response = await self.call('environmentsGet', path={'id': id})
        return EnvironmentsEnvironment.from_dict(response.data)

    async def environments_archive(self, id: str) -> Response:
        """Archive an environment."""
        return await self.call('environmentsArchive', path={'id': id})

    async def files_list(self, *, session_id: Optional[str] = None, task_id: Optional[str] = None, limit: Optional[int] = None, offset: Optional[int] = None) -> FilesListResponse:
        """List files."""
        query = {key: value for key, value in {'session_id': session_id, 'task_id': task_id, 'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('filesList', query=query)
        return FilesListResponse.from_dict(response.data)

    async def files_delete(self, id: str) -> Response:
        """Delete a file."""
        return await self.call('filesDelete', path={'id': id})

    async def files_get(self, id: str) -> FilesFile:
        """Get file metadata."""
        response = await self.call('filesGet', path={'id': id})
        return FilesFile.from_dict(response.data)

    async def memory_list_stores(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> MemoryListResponse:
        """List memory stores."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('memoryListStores', query=query)
        return MemoryListResponse.from_dict(response.data)

    async def memory_create_store(self, *, body: MemoryCreateStoreRequest) -> MemoryStore:
        """Create a memory store."""
        response = await self.call('memoryCreateStore', body=body.to_dict())
        return MemoryStore.from_dict(response.data)

    async def memory_list_conflicts(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> MemoryConflictsResponse:
        """List memory conflicts."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('memoryListConflicts', path={'id': id}, query=query)
        return MemoryConflictsResponse.from_dict(response.data)

    async def memory_delete_entry(self, id: str, *, body: MemoryDeleteEntryRequest) -> MemoryEntry:
        """Delete a memory entry."""
        response = await self.call('memoryDeleteEntry', path={'id': id}, body=body.to_dict())
        return MemoryEntry.from_dict(response.data)

    async def memory_list_entries(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> MemoryEntriesResponse:
        """List memory entries."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('memoryListEntries', path={'id': id}, query=query)
        return MemoryEntriesResponse.from_dict(response.data)

    async def memory_write_entry(self, id: str, *, body: MemoryWriteEntryRequest) -> MemoryEntry:
        """Write a memory entry."""
        response = await self.call('memoryWriteEntry', path={'id': id}, body=body.to_dict())
        return MemoryEntry.from_dict(response.data)

    async def memory_list_revisions(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> MemoryRevisionsResponse:
        """Memory revision history."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('memoryListRevisions', path={'id': id}, query=query)
        return MemoryRevisionsResponse.from_dict(response.data)

    async def execution_list_sessions(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionSessionsResponse:
        """List sessions."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('executionListSessions', query=query)
        return ExecutionSessionsResponse.from_dict(response.data)

    async def execution_create_session(self, *, body: ExecutionCreateSessionRequest) -> ExecutionSession:
        """Create a session."""
        response = await self.call('executionCreateSession', body=body.to_dict())
        return ExecutionSession.from_dict(response.data)

    async def execution_get_session(self, id: str) -> ExecutionSession:
        """Get a session."""
        response = await self.call('executionGetSession', path={'id': id})
        return ExecutionSession.from_dict(response.data)

    async def execution_archive_session(self, id: str) -> Response:
        """Archive a session."""
        return await self.call('executionArchiveSession', path={'id': id})

    async def execution_list_events(self, id: str, *, after: Optional[int] = None, last_event_id: Optional[str] = None) -> ExecutionEventsResponse:
        """List session events."""
        query = {key: value for key, value in {'after': after}.items() if value is not None}
        headers = {key: value for key, value in {'Last-Event-ID': last_event_id}.items() if value is not None}
        response = await self.call('executionListEvents', path={'id': id}, query=query, headers=headers)
        return ExecutionEventsResponse.from_dict(response.data)

    async def execution_messages(self, id: str, *, after: Optional[int] = None, task_id: Optional[str] = None, last_event_id: Optional[str] = None) -> ExecutionMessagesResponse:
        """Session message history."""
        query = {key: value for key, value in {'after': after, 'task_id': task_id}.items() if value is not None}
        headers = {key: value for key, value in {'Last-Event-ID': last_event_id}.items() if value is not None}
        response = await self.call('executionMessages', path={'id': id}, query=query, headers=headers)
        return ExecutionMessagesResponse.from_dict(response.data)

    async def execution_required_actions(self, id: str) -> ExecutionRequiredActionsResponse:
        """Required user actions."""
        response = await self.call('executionRequiredActions', path={'id': id})
        return ExecutionRequiredActionsResponse.from_dict(response.data)

    async def execution_list_tasks(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionTasksResponse:
        """List session tasks."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('executionListTasks', path={'id': id}, query=query)
        return ExecutionTasksResponse.from_dict(response.data)

    async def execution_create_task(self, id: str, *, body: ExecutionTaskRequest, idempotency_key: Optional[str] = None) -> ExecutionTask:
        """Create a task."""
        headers = {key: value for key, value in {'Idempotency-Key': idempotency_key}.items() if value is not None}
        response = await self.call('executionCreateTask', path={'id': id}, headers=headers, body=body.to_dict())
        return ExecutionTask.from_dict(response.data)

    async def skills_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> SkillsListResponse:
        """List skill packages."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('skillsList', query=query)
        return SkillsListResponse.from_dict(response.data)

    async def execution_get_task(self, id: str) -> ExecutionTask:
        """Get a task."""
        response = await self.call('executionGetTask', path={'id': id})
        return ExecutionTask.from_dict(response.data)

    async def execution_delegate_task(self, id: str, *, body: ExecutionTaskRequest, idempotency_key: Optional[str] = None) -> ExecutionTask:
        """Delegate a child task."""
        headers = {key: value for key, value in {'Idempotency-Key': idempotency_key}.items() if value is not None}
        response = await self.call('executionDelegateTask', path={'id': id}, headers=headers, body=body.to_dict())
        return ExecutionTask.from_dict(response.data)

    async def execution_cancel_task(self, id: str) -> Response:
        """Cancel a task."""
        return await self.call('executionCancelTask', path={'id': id})

    async def execution_collaboration(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionTasksResponse:
        """Collaboration tasks."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('executionCollaboration', path={'id': id}, query=query)
        return ExecutionTasksResponse.from_dict(response.data)

    async def execution_generations(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionGenerationsResponse:
        """Task model-call records."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('executionGenerations', path={'id': id}, query=query)
        return ExecutionGenerationsResponse.from_dict(response.data)

    async def execution_list_inputs(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> ExecutionInputsResponse:
        """List task inputs."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('executionListInputs', path={'id': id}, query=query)
        return ExecutionInputsResponse.from_dict(response.data)

    async def execution_add_input(self, id: str, *, body: ExecutionTextRequest, idempotency_key: Optional[str] = None) -> Response:
        """Steer a task with additional input."""
        headers = {key: value for key, value in {'Idempotency-Key': idempotency_key}.items() if value is not None}
        return await self.call('executionAddInput', path={'id': id}, headers=headers, body=body.to_dict())

    async def execution_reconcile(self, id: str, *, body: ExecutionReconcileRequest) -> Response:
        """Reconcile a task."""
        return await self.call('executionReconcile', path={'id': id}, body=body.to_dict())

    async def execution_resume_task(self, id: str, *, body: ExecutionTextRequest) -> Response:
        """Resume a terminal child task."""
        return await self.call('executionResumeTask', path={'id': id}, body=body.to_dict())

    async def execution_list_tools(self, id: str) -> ExecutionToolCallsResponse:
        """List task tool calls."""
        response = await self.call('executionListTools', path={'id': id})
        return ExecutionToolCallsResponse.from_dict(response.data)

    async def execution_resolve_tool_result(self, id: str, call: str, *, body: ExecutionToolResultRequest) -> Response:
        """Submit tool approval or result."""
        return await self.call('executionResolveToolResult', path={'id': id, 'call': call}, body=body.to_dict())

    async def execution_trace(self, id: str) -> ExecutionTrace:
        """Task trace and timing breakdown."""
        response = await self.call('executionTrace', path={'id': id})
        return ExecutionTrace.from_dict(response.data)

    async def vaults_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> VaultVaultsResponse:
        """List vaults."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('vaultsList', query=query)
        return VaultVaultsResponse.from_dict(response.data)

    async def vaults_create(self, *, body: VaultVaultRequest) -> VaultVault:
        """Create a vault."""
        response = await self.call('vaultsCreate', body=body.to_dict())
        return VaultVault.from_dict(response.data)

    async def vaults_archive(self, id: str) -> VaultVault:
        """Archive a vault."""
        response = await self.call('vaultsArchive', path={'id': id})
        return VaultVault.from_dict(response.data)

    async def vaults_get(self, id: str) -> VaultVault:
        """Get a vault."""
        response = await self.call('vaultsGet', path={'id': id})
        return VaultVault.from_dict(response.data)

    async def webhooks_list(self, *, limit: Optional[int] = None, offset: Optional[int] = None) -> WebhooksListResponse:
        """List webhooks."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('webhooksList', query=query)
        return WebhooksListResponse.from_dict(response.data)

    async def webhooks_create(self, *, body: WebhooksCreateRequest) -> WebhooksSubscription:
        """Create a webhook."""
        response = await self.call('webhooksCreate', body=body.to_dict())
        return WebhooksSubscription.from_dict(response.data)

    async def webhooks_get(self, id: str) -> WebhooksSubscription:
        """Get a webhook."""
        response = await self.call('webhooksGet', path={'id': id})
        return WebhooksSubscription.from_dict(response.data)

    async def webhooks_update(self, id: str, *, body: WebhooksUpdateRequest) -> WebhooksSubscription:
        """Pause or resume a webhook."""
        response = await self.call('webhooksUpdate', path={'id': id}, body=body.to_dict())
        return WebhooksSubscription.from_dict(response.data)

    async def webhooks_deliveries(self, id: str, *, limit: Optional[int] = None, offset: Optional[int] = None) -> WebhooksDeliveriesResponse:
        """Webhook deliveries."""
        query = {key: value for key, value in {'limit': limit, 'offset': offset}.items() if value is not None}
        response = await self.call('webhooksDeliveries', path={'id': id}, query=query)
        return WebhooksDeliveriesResponse.from_dict(response.data)

    async def webhooks_retry(self, id: str, delivery: str) -> WebhooksDelivery:
        """Retry delivery."""
        response = await self.call('webhooksRetry', path={'id': id, 'delivery': delivery})
        return WebhooksDelivery.from_dict(response.data)

