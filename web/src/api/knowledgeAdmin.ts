import { del, get, post, put } from '@/utils/request'
import { assertOk, type Paginated } from '@/api/mailAdmin'

export interface KnowledgeNamespaceRow {
  id: string
  orgId?: number
  namespace: string
  name: string
  description?: string
  embed_model: string
  vector_dim: number
  status: string
  created_at?: string
  updated_at?: string
}

export interface KnowledgeDocumentRow {
  id: string
  orgId?: number
  namespace: string
  title: string
  source?: string
  file_hash: string
  text_url?: string
  record_ids?: string
  status: string
  created_at?: string
  updated_at?: string
}

export interface KnowledgeNamespaceUpsertBody {
  namespace: string
  name: string
  description?: string
  embed_model: string
  vector_dim: number
  status?: string
}

export interface KnowledgeDocumentUpsertBody {
  namespace: string
  title: string
  source?: string
  file_hash: string
  record_ids?: string
  status?: string
}

export type KnowledgeNamespaceDetail = {
  namespace: KnowledgeNamespaceRow
}

export type KnowledgeDocumentDetail = {
  document: KnowledgeDocumentRow
}

const api = {
  namespaces: '/api/knowledge-namespaces',
  documents: '/api/knowledge-documents',
}

export async function listKnowledgeNamespaces(page: number, pageSize: number, status?: string) {
  const r = await get<Paginated<KnowledgeNamespaceRow>>(api.namespaces, {
    params: { page, pageSize, ...(status ? { status } : {}) },
  })
  return assertOk(r)
}

export async function createKnowledgeNamespace(body: KnowledgeNamespaceUpsertBody) {
  const r = await post<KnowledgeNamespaceRow>(api.namespaces, body)
  return assertOk(r)
}

export async function getKnowledgeNamespace(id: string | number) {
  const r = await get<KnowledgeNamespaceDetail>(`${api.namespaces}/${id}`)
  return assertOk(r)
}

export async function updateKnowledgeNamespace(id: string | number, body: KnowledgeNamespaceUpsertBody) {
  const r = await put<KnowledgeNamespaceRow>(`${api.namespaces}/${id}`, body)
  return assertOk(r)
}

export async function deleteKnowledgeNamespace(id: string | number) {
  const r = await del<{ id: number }>(`${api.namespaces}/${id}`)
  return assertOk(r)
}

export async function listKnowledgeDocuments(
  page: number,
  pageSize: number,
  options?: { namespace?: string; status?: string },
) {
  const r = await get<Paginated<KnowledgeDocumentRow>>(api.documents, {
    params: {
      page,
      pageSize,
      ...(options?.namespace ? { namespace: options.namespace } : {}),
      ...(options?.status ? { status: options.status } : {}),
    },
  })
  return assertOk(r)
}

export async function getKnowledgeDocument(id: string | number) {
  const r = await get<KnowledgeDocumentDetail>(`${api.documents}/${id}`)
  return assertOk(r)
}

export async function updateKnowledgeDocument(id: string | number, body: KnowledgeDocumentUpsertBody) {
  const r = await put<KnowledgeDocumentRow>(`${api.documents}/${id}`, body)
  return assertOk(r)
}

export async function deleteKnowledgeDocument(id: string | number) {
  const r = await del<{ id: number }>(`${api.documents}/${id}`)
  return assertOk(r)
}

export async function getKnowledgeDocumentText(id: string | number) {
  const r = await get<{ url: string; markdown: string }>(`${api.documents}/${id}/text`)
  return assertOk(r)
}

export async function updateKnowledgeDocumentText(id: string | number, markdown: string) {
  const r = await put<{ document: KnowledgeDocumentRow }>(`${api.documents}/${id}/text`, { markdown })
  return assertOk(r)
}

export async function uploadKnowledgeDocument(namespaceID: string | number, file: File) {
  const form = new FormData()
  form.append('file', file)
  const r = await post<{ document: KnowledgeDocumentRow }>(`${api.namespaces}/${namespaceID}/upload`, form)
  return assertOk(r)
}

export async function reuploadKnowledgeDocument(docID: string | number, file: File) {
  const form = new FormData()
  form.append('file', file)
  const r = await post<{ document: KnowledgeDocumentRow }>(`${api.documents}/${docID}/upload`, form)
  return assertOk(r)
}

export type RecallTestBody = {
  query: string
  topK?: number
  minScore?: number
  docId?: string
}

export async function recallTest(namespaceID: string | number, body: RecallTestBody) {
  const r = await post<{
    namespace: KnowledgeNamespaceRow
    query: string
    topK: number
    minScore: number
    document?: KnowledgeDocumentRow | null
    hits: number
    expected: number
    recall_at_k: number
    precision_at_k: number
    results: Array<{ record: { id: string; title: string; content: string }; score: number }>
  }>(`${api.namespaces}/${namespaceID}/recall-test`, body)
  return assertOk(r)
}
