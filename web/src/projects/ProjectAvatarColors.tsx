import { useMemo, type ReactNode } from "react";

import { AvatarColors } from "../ui/Avatar";
import { memberColors } from "../ui/avatar";
import { useProjectMembers } from "./hooks";

/**
 * Цвета аватаров участников текущего проекта (страницы проекта и карточка его задачи): разные у
 * разных участников. Вне проекта — цвет по id.
 */
export function ProjectAvatarColors({ projectKey, children }: { projectKey: string | null; children: ReactNode }) {
  const members = useProjectMembers(projectKey ?? "");
  const colors = useMemo(
    () => (projectKey && members.data ? memberColors(members.data.map((m) => m.user_id)) : null),
    [projectKey, members.data],
  );
  return <AvatarColors.Provider value={colors}>{children}</AvatarColors.Provider>;
}
