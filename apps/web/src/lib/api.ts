// REST 客户端：对接 Go 后端（apps/server-go）。取代原来的 lib/mock.ts。
import { env } from "@mistake/env/web";

import type { Mastery } from "./theme";

const BASE = env.VITE_SERVER_URL.replace(/\/$/, ""); // http://localhost:3000

// 共享密钥：生产环境构建时注入 VITE_API_KEY，随每个请求带上（本地为空则不带）
function authHeaders(): Record<string, string> {
  return env.VITE_API_KEY ? { "X-API-Key": env.VITE_API_KEY } : {};
}

export interface Mistake {
  _id: string;
  imageFileID: string;
  subject: string;
  knowledgePoints: string[];
  questionType: string;
  difficulty: string;
  ocrText: string;
  answer: string;
  errorReason: string;
  mastery: Mastery;
  wrongCount: number;
  kind: "photo" | "variant";
  fromMistakeId: string;
  createdAt: number;
}

export interface Stats {
  total: number;
  mastered: number;
  reviewing: number;
  unmastered: number;
  pending: number;
  streak: number;
  bySubject: { subject: string; count: number }[];
  weekly: { label: string; count: number }[];
}

export interface AdminData {
  userCount: number;
  mistakeCount: number;
  masteredCount: number;
  users: { openid: string; short: string; count: number; mastered: number }[];
}

export interface RecognizeResult {
  subject: string;
  knowledgePoints: string[];
  questionType: string;
  difficulty: string;
  ocrText: string;
  answer: string;
  errorReason: string;
}

export interface SimilarItem {
  question: string;
  answer: string;
  analysis: string;
}

export interface NewMistake {
  imageFileID: string;
  subject: string;
  knowledgePoints: string[];
  questionType: string;
  difficulty: string;
  ocrText: string;
  answer: string;
  errorReason: string;
  mastery: Mastery;
  wrongCount: number;
  kind?: "photo" | "variant";
  fromMistakeId?: string;
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${BASE}${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...authHeaders(), ...(init?.headers || {}) },
  });
  if (!res.ok) {
    let msg = `请求失败 (${res.status})`;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const api = {
  listMistakes(subject = "", limit?: number): Promise<Mistake[]> {
    const q = new URLSearchParams();
    if (subject) q.set("subject", subject);
    if (limit) q.set("limit", String(limit));
    const qs = q.toString();
    return req<Mistake[]>(`/api/mistakes${qs ? `?${qs}` : ""}`);
  },
  getMistake(id: string): Promise<Mistake> {
    return req<Mistake>(`/api/mistakes/${id}`);
  },
  createMistake(body: NewMistake): Promise<Mistake> {
    return req<Mistake>("/api/mistakes", { method: "POST", body: JSON.stringify(body) });
  },
  updateMastery(id: string, mastery: Mastery): Promise<Mistake> {
    return req<Mistake>(`/api/mistakes/${id}`, { method: "PATCH", body: JSON.stringify({ mastery }) });
  },
  grade(id: string, action: "unknown" | "fuzzy" | "mastered"): Promise<Mistake> {
    return req<Mistake>(`/api/mistakes/${id}/grade`, { method: "POST", body: JSON.stringify({ action }) });
  },
  deleteMistake(id: string): Promise<{ success: boolean }> {
    return req<{ success: boolean }>(`/api/mistakes/${id}`, { method: "DELETE" });
  },
  random(subject: string, mastery: string): Promise<{ data: Mistake | null; poolCount: number }> {
    const q = new URLSearchParams();
    if (subject) q.set("subject", subject);
    if (mastery) q.set("mastery", mastery);
    return req(`/api/random?${q.toString()}`);
  },
  stats(): Promise<Stats> {
    return req<Stats>("/api/stats");
  },
  admin(): Promise<AdminData> {
    return req<AdminData>("/api/admin");
  },
  async upload(file: File): Promise<{ imageFileID: string }> {
    const form = new FormData();
    form.append("file", file);
    const res = await fetch(`${BASE}/api/upload`, { method: "POST", body: form, headers: authHeaders() });
    if (!res.ok) throw new Error(`上传失败 (${res.status})`);
    return res.json();
  },
  recognize(imageFileID: string): Promise<RecognizeResult> {
    return req<RecognizeResult>("/api/recognize", {
      method: "POST",
      body: JSON.stringify({ imageFileID }),
    });
  },
  similar(input: {
    subject: string;
    knowledgePoints: string[];
    questionType: string;
    difficulty: string;
    ocrText: string;
    answer: string;
    count: number;
    exclude: string[];
  }): Promise<SimilarItem[]> {
    return req<SimilarItem[]>("/api/similar", { method: "POST", body: JSON.stringify(input) });
  },
  // 导出：POST 拿到 docx blob，触发浏览器下载
  async exportDocx(subject: string, ids: string[]): Promise<void> {
    const res = await fetch(`${BASE}/api/export`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...authHeaders() },
      body: JSON.stringify({ subject, ids }),
    });
    if (!res.ok) throw new Error(`导出失败 (${res.status})`);
    const blob = await res.blob();
    const cd = res.headers.get("Content-Disposition") || "";
    const match = cd.match(/filename="?([^"]+)"?/);
    const filename = match ? match[1] : "mistakes.docx";
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    a.remove();
    URL.revokeObjectURL(url);
  },
};

export function topicOf(m: Pick<Mistake, "knowledgePoints" | "questionType">): string {
  return (m.knowledgePoints && m.knowledgePoints.join("·")) || m.questionType || "错题";
}
