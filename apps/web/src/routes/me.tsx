import { useQuery } from "@tanstack/react-query";
import { createFileRoute } from "@tanstack/react-router";
import { BookOpen, Flame, Sparkles, Target } from "lucide-react";

import { api } from "@/lib/api";

export const Route = createFileRoute("/me")({
  component: MeComponent,
});

// 作业一：用「你的用户名」生成一个个人介绍。改这里即可换成你自己的用户名。
const USERNAME = "Tearl";
const TAGS = ["错题本", "Go 后端", "React 前端", "AWS 部署"];

// 由用户名程序化生成一段个人介绍
function genIntro(name: string): string {
  const initial = name.charAt(0).toUpperCase();
  return `你好，我是 ${name}（${initial} 同学）。这是「拾错」错题本项目的作者页面——${name} 用 React + TanStack Router 写前端、Go + PostgreSQL 写后端，并把整套部署到了 AWS（ECS Fargate + ALB + RDS + S3）与 Cloudflare Pages。相信「把错题吃透，比多刷题更重要」。`;
}

function MeComponent() {
  const stats = useQuery({ queryKey: ["stats"], queryFn: api.stats }).data ?? {
    total: 0,
    mastered: 0,
    reviewing: 0,
    unmastered: 0,
    pending: 0,
    streak: 0,
    bySubject: [],
    weekly: [],
  };

  const cards = [
    { label: "我的错题", value: stats.total, Icon: BookOpen },
    { label: "已掌握", value: stats.mastered, Icon: Target },
    { label: "连续天数", value: stats.streak, Icon: Flame },
  ];

  return (
    <div className="px-4 pt-5">
      <h1 className="mb-5 h-serif text-[22px]">我的</h1>

      {/* 个人名片 */}
      <div className="rounded-2xl bg-[var(--c-dark)] p-5 text-white shadow-sm">
        <div className="flex items-center gap-4">
          <div className="flex h-16 w-16 items-center justify-center rounded-full bg-[var(--c-primary)] text-2xl font-semibold">
            {USERNAME.charAt(0).toUpperCase()}
          </div>
          <div>
            <div className="text-lg font-semibold">{USERNAME}</div>
            <div className="text-xs text-white/60">@{USERNAME.toLowerCase()}</div>
          </div>
        </div>
        <div className="mt-3 flex flex-wrap gap-2">
          {TAGS.map((t) => (
            <span key={t} className="rounded-full bg-white/10 px-3 py-1 text-[11px]">
              {t}
            </span>
          ))}
        </div>
      </div>

      {/* 统计 */}
      <div className="mt-4 grid grid-cols-3 gap-3">
        {cards.map(({ label, value, Icon }) => (
          <div
            key={label}
            className="flex flex-col items-center gap-1 rounded-2xl border border-[var(--c-border)] bg-white py-4"
          >
            <Icon size={18} className="text-[var(--c-primary)]" strokeWidth={2} />
            <span className="text-xl font-semibold text-[var(--c-dark)]">{value}</span>
            <span className="text-[11px] text-[var(--c-muted)]">{label}</span>
          </div>
        ))}
      </div>

      {/* 个人介绍 */}
      <section className="mt-4 rounded-2xl border border-[var(--c-border)] bg-white p-5">
        <div className="mb-2 flex items-center gap-1.5">
          <Sparkles size={16} className="text-[var(--c-primary)]" />
          <span className="text-sm font-semibold text-[var(--c-dark)]">个人介绍</span>
        </div>
        <p className="text-sm leading-7 text-[var(--c-muted)]">{genIntro(USERNAME)}</p>
      </section>

      <p className="mt-4 text-center text-[11px] text-[var(--c-muted)]">
        本页由用户名 <span className="font-semibold text-[var(--c-primary)]">{USERNAME}</span> 生成
      </p>
    </div>
  );
}
