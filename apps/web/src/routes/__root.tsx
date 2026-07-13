import { Toaster } from "@mistake/ui/components/sonner";
import type { QueryClient } from "@tanstack/react-query";
import { HeadContent, Outlet, createRootRouteWithContext } from "@tanstack/react-router";

import TabBar from "@/components/tab-bar";

import "../index.css";

export interface RouterAppContext {
  queryClient: QueryClient;
}

export const Route = createRootRouteWithContext<RouterAppContext>()({
  component: RootComponent,
  head: () => ({
    meta: [
      {
        title: "拾错 · 错题本",
      },
      {
        name: "description",
        content: "拍照上传错题，AI 识别归类，随机抽题复习，学习统计。",
      },
      {
        name: "viewport",
        content: "width=device-width, initial-scale=1, maximum-scale=1",
      },
    ],
    links: [
      {
        rel: "icon",
        href: "/favicon.ico",
      },
    ],
  }),
});

function RootComponent() {
  return (
    <>
      <HeadContent />
      <div className="mistake-app relative mx-auto min-h-svh max-w-[448px] pb-[calc(72px+env(safe-area-inset-bottom))] shadow-xl">
        <Outlet />
      </div>
      <TabBar />
      <Toaster richColors position="top-center" />
    </>
  );
}
