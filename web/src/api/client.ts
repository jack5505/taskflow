/* eslint-disable */
/* tslint:disable */
// @ts-nocheck
/*
 * ---------------------------------------------------------------
 * ## THIS FILE WAS GENERATED VIA SWAGGER-TYPESCRIPT-API        ##
 * ##                                                           ##
 * ## AUTHOR: acacode                                           ##
 * ## SOURCE: https://github.com/acacode/swagger-typescript-api ##
 * ---------------------------------------------------------------
 */

export interface ApierrBody {
  /** @example "INVALID_TRANSITION" */
  code?: string;
  details?: Record<string, any>;
  /** @example "Переход done → todo не разрешён" */
  message?: string;
}

export interface ApierrResponse {
  error?: ApierrBody;
}

export interface AuditEntry {
  action?: string;
  actor_id?: string;
  created_at?: string;
  entity_id?: string;
  entity_type?: string;
  id?: string;
  new_value?: object;
  old_value?: object;
  project_id?: string;
  source?: "web" | "bot" | "cli" | "system";
}

export interface AuditPage {
  items?: AuditEntry[];
  next_cursor?: string;
}

export interface AuthSessionResponse {
  access_token?: string;
  /** @example 900 */
  expires_in?: number;
  /** @example "Bearer" */
  token_type?: string;
  user?: UserUser;
}

export interface AuthLoginRequest {
  /** @maxLength 254 */
  email: string;
  /** @maxLength 200 */
  password: string;
}

export interface AuthPasswordResetRequest {
  /** @maxLength 254 */
  email: string;
}

export interface AuthPasswordSetRequest {
  /**
   * @minLength 10
   * @maxLength 72
   */
  password: string;
  /** @maxLength 200 */
  token: string;
}

export interface MeMyProject {
  id?: string;
  is_archived?: boolean;
  key?: string;
  name?: string;
  role?: "manager" | "member";
}

export interface MeProfile {
  created_at?: string;
  display_name?: string;
  email?: string;
  id?: string;
  is_active?: boolean;
  is_system_admin?: boolean;
  notification_settings?: Record<string, boolean>;
  projects?: MeMyProject[];
  telegram_user_id?: number;
  telegram_username?: string;
  timer?: object;
  timezone?: string;
  updated_at?: string;
}

export interface MeUpdate {
  /** @maxLength 200 */
  display_name?: string;
  notification_settings?: Record<string, boolean>;
  /**
   * @maxLength 64
   * @example "Europe/Moscow"
   */
  timezone?: string;
}

export interface MeReadRequest {
  ids?: string[];
}

export interface MeReadResponse {
  marked?: number;
}

export interface NotificationNotification {
  created_at?: string;
  id?: string;
  payload?: object;
  read_at?: string;
  task_id?: string;
  type?: string;
}

export interface NotificationPage {
  items?: NotificationNotification[];
  next_cursor?: string;
  unread_count?: number;
}

export interface ProjectActiveSprint {
  /** @example "2026-10-14" */
  end_date?: string;
  id?: string;
  name?: string;
  /** @example "2026-10-01" */
  start_date?: string;
}

export interface ProjectListItem {
  active_sprint?: ProjectActiveSprint;
  created_at?: string;
  daily_summary_time?: string;
  /** @format date-time */
  deleted_at?: string;
  description?: string;
  id?: string;
  is_archived?: boolean;
  key?: string;
  my_open_tasks?: number;
  my_role?: string;
  name?: string;
  notification_settings?: Record<string, boolean>;
  open_tasks?: number;
  telegram_chat_id?: number;
  timezone?: string;
  updated_at?: string;
}

export interface ProjectMemberView {
  display_name?: string;
  email?: string;
  is_active?: boolean;
  role?: "manager" | "member";
  user_id?: string;
}

export interface ProjectView {
  created_at?: string;
  daily_summary_time?: string;
  /** @format date-time */
  deleted_at?: string;
  description?: string;
  id?: string;
  is_archived?: boolean;
  key?: string;
  my_role?: string;
  name?: string;
  notification_settings?: Record<string, boolean>;
  telegram_chat_id?: number;
  timezone?: string;
  updated_at?: string;
}

export interface ProjectCreateRequest {
  /** @maxLength 2000 */
  description?: string;
  /**
   * @maxLength 10
   * @example "TM"
   */
  key: string;
  /** @maxLength 100 */
  name: string;
  /**
   * @maxLength 64
   * @example "Europe/Moscow"
   */
  timezone?: string;
}

export interface ProjectPutMemberRequest {
  /** допустимость проверяет сервис — одно правило и для бота */
  role: "manager" | "member";
}

export interface ProjectUpdateRequest {
  /**
   * @maxLength 5
   * @example "10:00"
   */
  daily_summary_time?: string;
  /** @maxLength 2000 */
  description?: string;
  is_archived?: boolean;
  /** @maxLength 100 */
  name?: string;
  notification_settings?: Record<string, boolean>;
  /** @maxLength 64 */
  timezone?: string;
}

export interface SprintScope {
  estimate_minutes?: number;
  tasks?: number;
  without_estimate?: number;
}

export interface SprintView {
  closed_at?: string;
  created_at?: string;
  /** @example "2026-10-24" */
  end_date?: string;
  estimate_minutes?: number;
  goal?: string;
  id?: string;
  name?: string;
  overdue?: boolean;
  project_id?: string;
  /** @example "2026-10-13" */
  start_date?: string;
  state?: "planned" | "active" | "closed";
  tasks_count?: number;
  updated_at?: string;
}

export interface SprintRequest {
  /** @example "2026-10-24" */
  end_date?: string | null;
  goal?: string | null;
  /** @maxLength 300 */
  name?: string;
  /** @example "2026-10-13" */
  start_date?: string | null;
}

export interface SprintStartResponse {
  scope?: SprintScope;
  sprint?: SprintView;
}

export interface TaskCommentView {
  author_id?: string;
  author_name?: string;
  body?: string;
  created_at?: string;
  edited?: boolean;
  id?: string;
  source?: "web" | "telegram";
  task_id?: string;
  updated_at?: string;
}

export interface TaskHistoryView {
  created_at?: string;
  field?: string;
  id?: string;
  new_value?: any;
  /** @example "снят при деактивации" */
  note?: string;
  old_value?: any;
  source?: "web" | "telegram" | "api";
  user_id?: string;
}

export interface TaskListItem {
  assignee_id?: string;
  completed_at?: string;
  created_at?: string;
  deleted_at?: string;
  description?: string;
  /** @example "2026-10-31" */
  due_date?: string;
  estimate_minutes?: number;
  id?: string;
  /** @example "TM-42" */
  key?: string;
  labels?: string[];
  number?: number;
  overdue?: boolean;
  position?: number;
  priority?: "low" | "medium" | "high" | "critical";
  project_id?: string;
  reporter_id?: string;
  spent_minutes?: number;
  sprint_id?: string;
  started_at?: string;
  status?: "todo" | "in_progress" | "review" | "done" | "cancelled";
  title?: string;
  type?: "task" | "bug" | "feature";
  updated_at?: string;
  version?: number;
}

export interface TaskMyTask {
  /** @example "2026-10-31" */
  due_date?: string;
  estimate_minutes?: number;
  key?: string;
  overdue?: boolean;
  project_key?: string;
  project_name?: string;
  spent_minutes?: number;
  sprint_id?: string;
  status?: string;
  title?: string;
  updated_at?: string;
}

export interface TaskPage {
  items?: TaskListItem[];
  next_cursor?: string;
}

export interface TaskTask {
  assignee_id?: string;
  completed_at?: string;
  created_at?: string;
  deleted_at?: string;
  description?: string;
  /** @example "2026-10-31" */
  due_date?: string;
  estimate_minutes?: number;
  id?: string;
  /** @example "TM-42" */
  key?: string;
  labels?: string[];
  number?: number;
  position?: number;
  priority?: "low" | "medium" | "high" | "critical";
  project_id?: string;
  reporter_id?: string;
  spent_minutes?: number;
  sprint_id?: string;
  started_at?: string;
  status?: "todo" | "in_progress" | "review" | "done" | "cancelled";
  title?: string;
  type?: "task" | "bug" | "feature";
  updated_at?: string;
  version?: number;
}

export interface TaskCommentRequest {
  /**
   * 1–5000 после обрезки пробелов — сервис
   * @maxLength 20000
   */
  body: string;
}

export interface TaskCreateRequest {
  assignee_id?: string;
  /** @maxLength 20000 */
  description?: string;
  /** @example "2026-10-31" */
  due_date?: string;
  /**
   * @min 1
   * @max 60000
   */
  estimate_minutes?: number;
  /**
   * ≤10 по ≤30 после обрезки и без повторов — проверяет сервис
   * @maxItems 50
   */
  labels?: string[];
  priority?: "low" | "medium" | "high" | "critical";
  sprint_id?: string;
  /** @maxLength 200 */
  title: string;
  type?: "task" | "bug" | "feature";
}

export interface TaskMoveRequest {
  /** @format uuid */
  after_task_id?: string | null;
  /** @format uuid */
  sprint_id?: string | null;
}

export interface TaskTransitionRequest {
  assignee_id?: string;
  /** @maxLength 5000 */
  comment?: string;
  to: "todo" | "in_progress" | "review" | "done" | "cancelled";
}

export interface TaskUpdateRequest {
  /** @format uuid */
  assignee_id?: string | null;
  description?: string | null;
  /** @example "2026-10-31" */
  due_date?: string | null;
  estimate_minutes?: number | null;
  labels?: string[];
  priority?: "low" | "medium" | "high" | "critical";
  /** @format uuid */
  sprint_id?: string | null;
  title?: string;
  type?: "task" | "bug" | "feature";
}

export interface UserUser {
  created_at?: string;
  display_name?: string;
  email?: string;
  id?: string;
  is_active?: boolean;
  is_system_admin?: boolean;
  notification_settings?: object;
  telegram_user_id?: number;
  telegram_username?: string;
  timezone?: string;
  updated_at?: string;
}

export interface UsersCreateRequest {
  /**
   * 1–80 после обрезки пробелов проверяет сервис
   * @maxLength 200
   */
  display_name: string;
  /** @maxLength 254 */
  email: string;
  is_system_admin?: boolean;
  /**
   * @maxLength 64
   * @example "Europe/Moscow"
   */
  timezone?: string;
}

export interface UsersUpdateRequest {
  /** @maxLength 200 */
  display_name?: string;
  is_active?: boolean;
  is_system_admin?: boolean;
  /** @maxLength 64 */
  timezone?: string;
}

export type QueryParamsType = Record<string | number, any>;
export type ResponseFormat = keyof Omit<Body, "body" | "bodyUsed">;

export interface FullRequestParams extends Omit<RequestInit, "body"> {
  /** set parameter to `true` for call `securityWorker` for this request */
  secure?: boolean;
  /** request path */
  path: string;
  /** content type of request body */
  type?: ContentType;
  /** query params */
  query?: QueryParamsType;
  /** format of response (i.e. response.json() -> format: "json") */
  format?: ResponseFormat;
  /** request body */
  body?: unknown;
  /** base url */
  baseUrl?: string;
  /** request cancellation token */
  cancelToken?: CancelToken;
}

export type RequestParams = Omit<
  FullRequestParams,
  "body" | "method" | "query" | "path"
>;

export interface ApiConfig<SecurityDataType = unknown> {
  baseUrl?: string;
  baseApiParams?: Omit<RequestParams, "baseUrl" | "cancelToken" | "signal">;
  securityWorker?: (
    securityData: SecurityDataType | null,
  ) => Promise<RequestParams | void> | RequestParams | void;
  customFetch?: typeof fetch;
}

export interface HttpResponse<D extends unknown, E extends unknown = unknown>
  extends Response {
  data: D;
  error: E;
}

type CancelToken = Symbol | string | number;

export enum ContentType {
  Json = "application/json",
  JsonApi = "application/vnd.api+json",
  FormData = "multipart/form-data",
  UrlEncoded = "application/x-www-form-urlencoded",
  Text = "text/plain",
}

export class HttpClient<SecurityDataType = unknown> {
  public baseUrl: string = "/";
  private securityData: SecurityDataType | null = null;
  private securityWorker?: ApiConfig<SecurityDataType>["securityWorker"];
  private abortControllers = new Map<CancelToken, AbortController>();
  private customFetch = (...fetchParams: Parameters<typeof fetch>) =>
    fetch(...fetchParams);

  private baseApiParams: RequestParams = {
    credentials: "same-origin",
    headers: {},
    redirect: "follow",
    referrerPolicy: "no-referrer",
  };

  constructor(apiConfig: ApiConfig<SecurityDataType> = {}) {
    Object.assign(this, apiConfig);
  }

  public setSecurityData = (data: SecurityDataType | null) => {
    this.securityData = data;
  };

  protected encodeQueryParam(key: string, value: any) {
    const encodedKey = encodeURIComponent(key);
    return `${encodedKey}=${encodeURIComponent(typeof value === "number" ? value : `${value}`)}`;
  }

  protected addQueryParam(query: QueryParamsType, key: string) {
    return this.encodeQueryParam(key, query[key]);
  }

  protected addArrayQueryParam(query: QueryParamsType, key: string) {
    const value = query[key];
    return value.map((v: any) => this.encodeQueryParam(key, v)).join("&");
  }

  protected toQueryString(rawQuery?: QueryParamsType): string {
    const query = rawQuery || {};
    const keys = Object.keys(query).filter(
      (key) => "undefined" !== typeof query[key],
    );
    return keys
      .map((key) =>
        Array.isArray(query[key])
          ? this.addArrayQueryParam(query, key)
          : this.addQueryParam(query, key),
      )
      .join("&");
  }

  protected addQueryParams(rawQuery?: QueryParamsType): string {
    const queryString = this.toQueryString(rawQuery);
    return queryString ? `?${queryString}` : "";
  }

  private contentFormatters: Record<ContentType, (input: any) => any> = {
    [ContentType.Json]: (input: any) =>
      input !== null && (typeof input === "object" || typeof input === "string")
        ? JSON.stringify(input)
        : input,
    [ContentType.JsonApi]: (input: any) =>
      input !== null && (typeof input === "object" || typeof input === "string")
        ? JSON.stringify(input)
        : input,
    [ContentType.Text]: (input: any) =>
      input !== null && typeof input !== "string"
        ? JSON.stringify(input)
        : input,
    [ContentType.FormData]: (input: any) => {
      if (input instanceof FormData) {
        return input;
      }

      return Object.keys(input || {}).reduce((formData, key) => {
        const property = input[key];
        formData.append(
          key,
          property instanceof Blob
            ? property
            : typeof property === "object" && property !== null
              ? JSON.stringify(property)
              : `${property}`,
        );
        return formData;
      }, new FormData());
    },
    [ContentType.UrlEncoded]: (input: any) => this.toQueryString(input),
  };

  protected mergeRequestParams(
    params1: RequestParams,
    params2?: RequestParams,
  ): RequestParams {
    return {
      ...this.baseApiParams,
      ...params1,
      ...(params2 || {}),
      headers: {
        ...(this.baseApiParams.headers || {}),
        ...(params1.headers || {}),
        ...((params2 && params2.headers) || {}),
      },
    };
  }

  protected createAbortSignal = (
    cancelToken: CancelToken,
  ): AbortSignal | undefined => {
    if (this.abortControllers.has(cancelToken)) {
      const abortController = this.abortControllers.get(cancelToken);
      if (abortController) {
        return abortController.signal;
      }
      return void 0;
    }

    const abortController = new AbortController();
    this.abortControllers.set(cancelToken, abortController);
    return abortController.signal;
  };

  public abortRequest = (cancelToken: CancelToken) => {
    const abortController = this.abortControllers.get(cancelToken);

    if (abortController) {
      abortController.abort();
      this.abortControllers.delete(cancelToken);
    }
  };

  public request = async <T = any, E = any>({
    body,
    secure,
    path,
    type,
    query,
    format,
    baseUrl,
    cancelToken,
    ...params
  }: FullRequestParams): Promise<HttpResponse<T, E>> => {
    const secureParams =
      ((typeof secure === "boolean" ? secure : this.baseApiParams.secure) &&
        this.securityWorker &&
        (await this.securityWorker(this.securityData))) ||
      {};
    const requestParams = this.mergeRequestParams(params, secureParams);
    const queryString = query && this.toQueryString(query);
    const payloadFormatter = this.contentFormatters[type || ContentType.Json];
    const responseFormat = format || requestParams.format;

    return this.customFetch(
      `${baseUrl || this.baseUrl || ""}${path}${queryString ? `?${queryString}` : ""}`,
      {
        ...requestParams,
        headers: {
          ...(requestParams.headers || {}),
          ...(type && type !== ContentType.FormData
            ? { "Content-Type": type }
            : {}),
        },
        signal:
          (cancelToken
            ? this.createAbortSignal(cancelToken)
            : requestParams.signal) || null,
        body:
          typeof body === "undefined" || body === null
            ? null
            : payloadFormatter(body),
      },
    ).then(async (response) => {
      const r = response as HttpResponse<T, E>;
      r.data = null as unknown as T;
      r.error = null as unknown as E;

      const responseToParse = responseFormat ? response.clone() : response;
      const data = !responseFormat
        ? r
        : await responseToParse[responseFormat]()
            .then((data) => {
              if (r.ok) {
                r.data = data;
              } else {
                r.error = data;
              }
              return r;
            })
            .catch((e) => {
              r.error = e;
              return r;
            });

      if (cancelToken) {
        this.abortControllers.delete(cancelToken);
      }

      if (!response.ok) throw data;
      return data;
    });
  };
}

/**
 * @title TaskFlow API
 * @version 1.0
 * @baseUrl /
 * @contact
 *
 * Система управления задачами и временем с Telegram-ботом. Единый формат ошибок — ТЗ §9.3.
 */
export class Api<
  SecurityDataType extends unknown,
> extends HttpClient<SecurityDataType> {
  api = {
    /**
     * @description Возвращает access-токен (15 минут) и ставит httpOnly cookie refresh_token (30 дней). После 10 неудачных попыток вход по email закрыт на 15 минут.
     *
     * @tags auth
     * @name V1AuthLoginCreate
     * @summary Вход
     * @request POST:/api/v1/auth/login
     */
    v1AuthLoginCreate: (body: AuthLoginRequest, params: RequestParams = {}) =>
      this.request<AuthSessionResponse, ApierrResponse>({
        path: `/api/v1/auth/login`,
        method: "POST",
        body: body,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Отзывает refresh-токен из cookie и очищает её. Требует X-Requested-With: XMLHttpRequest.
     *
     * @tags auth
     * @name V1AuthLogoutCreate
     * @summary Выйти
     * @request POST:/api/v1/auth/logout
     */
    v1AuthLogoutCreate: (params: RequestParams = {}) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/auth/logout`,
        method: "POST",
        ...params,
      }),

    /**
     * No description
     *
     * @tags auth
     * @name V1AuthLogoutAllCreate
     * @summary Выйти везде
     * @request POST:/api/v1/auth/logout-all
     * @secure
     */
    v1AuthLogoutAllCreate: (params: RequestParams = {}) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/auth/logout-all`,
        method: "POST",
        secure: true,
        ...params,
      }),

    /**
     * @description Отправляет на email ссылку для установки нового пароля (действует 24 часа, одноразовая). Ответ всегда 204, даже если такого пользователя нет; повторный запрос на тот же email в течение минуты письма не шлёт.
     *
     * @tags auth
     * @name V1AuthPasswordResetCreate
     * @summary Восстановить пароль
     * @request POST:/api/v1/auth/password-reset
     */
    v1AuthPasswordResetCreate: (
      body: AuthPasswordResetRequest,
      params: RequestParams = {},
    ) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/auth/password-reset`,
        method: "POST",
        body: body,
        type: ContentType.Json,
        ...params,
      }),

    /**
     * @description Токен из ссылки одноразовый: после успешной установки повторно не принимается. Все сессии пользователя отзываются.
     *
     * @tags auth
     * @name V1AuthPasswordSetCreate
     * @summary Задать пароль по ссылке
     * @request POST:/api/v1/auth/password-set
     */
    v1AuthPasswordSetCreate: (
      body: AuthPasswordSetRequest,
      params: RequestParams = {},
    ) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/auth/password-set`,
        method: "POST",
        body: body,
        type: ContentType.Json,
        ...params,
      }),

    /**
     * @description Требует cookie refresh_token и заголовок X-Requested-With: XMLHttpRequest (защита от CSRF). Старый refresh-токен отзывается.
     *
     * @tags auth
     * @name V1AuthRefreshCreate
     * @summary Обновить access-токен
     * @request POST:/api/v1/auth/refresh
     */
    v1AuthRefreshCreate: (params: RequestParams = {}) =>
      this.request<AuthSessionResponse, ApierrResponse>({
        path: `/api/v1/auth/refresh`,
        method: "POST",
        format: "json",
        ...params,
      }),

    /**
     * @description Автор комментария или менеджер проекта (§2: чужие — только менеджер).
     *
     * @tags comments
     * @name V1CommentsDelete
     * @summary Удалить комментарий
     * @request DELETE:/api/v1/comments/{id}
     * @secure
     */
    v1CommentsDelete: (id: string, params: RequestParams = {}) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/comments/${id}`,
        method: "DELETE",
        secure: true,
        ...params,
      }),

    /**
     * @description Автор комментария или менеджер проекта (§3.6).
     *
     * @tags comments
     * @name V1CommentsPartialUpdate
     * @summary Изменить комментарий
     * @request PATCH:/api/v1/comments/{id}
     * @secure
     */
    v1CommentsPartialUpdate: (
      id: string,
      body: TaskCommentRequest,
      params: RequestParams = {},
    ) =>
      this.request<TaskCommentView, ApierrResponse>({
        path: `/api/v1/comments/${id}`,
        method: "PATCH",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Текущий пользователь, его личные уведомления §8.7 (все ключи, с умолчаниями), проекты и роли, запущенный таймер (пока всегда null — появится вместе с таймером).
     *
     * @tags me
     * @name V1MeList
     * @summary Профиль
     * @request GET:/api/v1/me
     * @secure
     */
    v1MeList: (params: RequestParams = {}) =>
      this.request<MeProfile, ApierrResponse>({
        path: `/api/v1/me`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Меняются только переданные поля. notification_settings — частичный объект личных уведомлений §8.7: assigned, rework, accepted_cancelled, comment, review_requested, due, timer_auto_stopped, worklog_trimmed, task_deleted, membership.
     *
     * @tags me
     * @name V1MePartialUpdate
     * @summary Изменить профиль
     * @request PATCH:/api/v1/me
     * @secure
     */
    v1MePartialUpdate: (body: MeUpdate, params: RequestParams = {}) =>
      this.request<MeProfile, ApierrResponse>({
        path: `/api/v1/me`,
        method: "PATCH",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Уведомления колокольчика за 30 дней, новые первыми. unread_count — только на первой странице (без cursor). Следующая страница — cursor из next_cursor.
     *
     * @tags me
     * @name V1MeNotificationsList
     * @summary Уведомления
     * @request GET:/api/v1/me/notifications
     * @secure
     */
    v1MeNotificationsList: (
      query?: {
        /** только непрочитанные */
        unread?: boolean;
        /** до 200, по умолчанию 50 */
        limit?: number;
        /** курсор следующей страницы */
        cursor?: string;
      },
      params: RequestParams = {},
    ) =>
      this.request<NotificationPage, ApierrResponse>({
        path: `/api/v1/me/notifications`,
        method: "GET",
        query: query,
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description ids — какие отметить (до 200); без ids или пустой список — все. Чужие id игнорируются.
     *
     * @tags me
     * @name V1MeNotificationsReadCreate
     * @summary Отметить уведомления прочитанными
     * @request POST:/api/v1/me/notifications/read
     * @secure
     */
    v1MeNotificationsReadCreate: (
      body: MeReadRequest,
      params: RequestParams = {},
    ) =>
      this.request<MeReadResponse, ApierrResponse>({
        path: `/api/v1/me/notifications/read`,
        method: "POST",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Открытые задачи текущего пользователя по всем его проектам, кроме архивных (§7.2), по сроку: без срока — в конце, просроченные — сверху. Не больше 200 самых срочных.
     *
     * @tags tasks
     * @name V1MeTasksList
     * @summary Мои задачи
     * @request GET:/api/v1/me/tasks
     * @secure
     */
    v1MeTasksList: (params: RequestParams = {}) =>
      this.request<TaskMyTask[], ApierrResponse>({
        path: `/api/v1/me/tasks`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * No description
     *
     * @tags system
     * @name V1PingList
     * @summary Проверка связи
     * @request GET:/api/v1/ping
     */
    v1PingList: (params: RequestParams = {}) =>
      this.request<Record<string, boolean>, any>({
        path: `/api/v1/ping`,
        method: "GET",
        format: "json",
        ...params,
      }),

    /**
     * @description Проекты, где пользователь участник; администратору системы — все. По умолчанию — не архивные; archived=true — только архив; deleted=true — только удалённые (администратор). В карточке — активный спринт (null, если нет), число открытых задач (todo, in_progress, review) и открытых задач на текущем пользователе (§7.2).
     *
     * @tags projects
     * @name V1ProjectsList
     * @summary Проекты
     * @request GET:/api/v1/projects
     * @secure
     */
    v1ProjectsList: (
      query?: {
        /** только архивные */
        archived?: boolean;
        /** только удалённые (администратор) */
        deleted?: boolean;
      },
      params: RequestParams = {},
    ) =>
      this.request<ProjectListItem[], ApierrResponse>({
        path: `/api/v1/projects`,
        method: "GET",
        query: query,
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Ключ — 2–10 заглавных латинских букв, уникален и среди удалённых проектов, не меняется. Участников добавляет администратор или менеджер (§9.2, участники).
     *
     * @tags projects
     * @name V1ProjectsCreate
     * @summary Создать проект
     * @request POST:/api/v1/projects
     * @secure
     */
    v1ProjectsCreate: (
      body: ProjectCreateRequest,
      params: RequestParams = {},
    ) =>
      this.request<ProjectView, ApierrResponse>({
        path: `/api/v1/projects`,
        method: "POST",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * No description
     *
     * @tags projects
     * @name V1ProjectsDetail
     * @summary Проект
     * @request GET:/api/v1/projects/{key}
     * @secure
     */
    v1ProjectsDetail: (key: string, params: RequestParams = {}) =>
      this.request<ProjectView, ApierrResponse>({
        path: `/api/v1/projects/${key}`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Мягкое удаление (§3.2): проект пропадает из списков, бота и отчётов, Telegram-группа отключается; данные сохраняются. Восстановление — POST /projects/{key}/restore.
     *
     * @tags projects
     * @name V1ProjectsDelete
     * @summary Удалить проект
     * @request DELETE:/api/v1/projects/{key}
     * @secure
     */
    v1ProjectsDelete: (key: string, params: RequestParams = {}) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/projects/${key}`,
        method: "DELETE",
        secure: true,
        ...params,
      }),

    /**
     * @description Менеджер проекта или администратор. Меняются только переданные поля; description "" очищает. notification_settings — частичный объект событий §8.6 (task_created, task_taken, task_returned, task_review, task_rework, task_accepted, task_cancelled, sprint_started, sprint_closed, daily_summary, comment_created). смену is_archived — только администратор (то же значение, что сейчас, пропускается). В архивном проекте любое изменение, кроме разархивации, — 409 PROJECT_ARCHIVED.
     *
     * @tags projects
     * @name V1ProjectsPartialUpdate
     * @summary Изменить проект
     * @request PATCH:/api/v1/projects/{key}
     * @secure
     */
    v1ProjectsPartialUpdate: (
      key: string,
      body: ProjectUpdateRequest,
      params: RequestParams = {},
    ) =>
      this.request<ProjectView, ApierrResponse>({
        path: `/api/v1/projects/${key}`,
        method: "PATCH",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Действия менеджеров и администраторов (ТЗ §3.8): участники и роли, настройки, архив, удаление и восстановление. Только администратор системы; доступен и для удалённого проекта. Новые записи первыми; следующая страница — cursor из next_cursor. Даты from/to — по часовому поясу проекта.
     *
     * @tags projects
     * @name V1ProjectsAuditList
     * @summary Журнал проекта
     * @request GET:/api/v1/projects/{key}/audit
     * @secure
     */
    v1ProjectsAuditList: (
      key: string,
      query?: {
        /** с (ГГГГ-ММ-ДД или RFC 3339) */
        from?: string;
        /** по (ГГГГ-ММ-ДД включительно или RFC 3339 не включая) */
        to?: string;
        /** кто действовал */
        user_id?: string;
        /** тип действия, например member.added */
        action?: string;
        /** до 200, по умолчанию 50 */
        limit?: number;
        /** курсор следующей страницы */
        cursor?: string;
      },
      params: RequestParams = {},
    ) =>
      this.request<AuditPage, ApierrResponse>({
        path: `/api/v1/projects/${key}/audit`,
        method: "GET",
        query: query,
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * No description
     *
     * @tags projects
     * @name V1ProjectsMembersList
     * @summary Участники проекта
     * @request GET:/api/v1/projects/{key}/members
     * @secure
     */
    v1ProjectsMembersList: (key: string, params: RequestParams = {}) =>
      this.request<ProjectMemberView[], ApierrResponse>({
        path: `/api/v1/projects/${key}/members`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Менеджер проекта или администратор (§2). Добавить можно только активного пользователя. Понижение единственного активного менеджера — 409 LAST_MANAGER. В архивном проекте — 409 PROJECT_ARCHIVED.
     *
     * @tags projects
     * @name V1ProjectsMembersUpdate
     * @summary Добавить участника или изменить роль
     * @request PUT:/api/v1/projects/{key}/members/{user_id}
     * @secure
     */
    v1ProjectsMembersUpdate: (
      key: string,
      userId: string,
      body: ProjectPutMemberRequest,
      params: RequestParams = {},
    ) =>
      this.request<ProjectMemberView, ApierrResponse>({
        path: `/api/v1/projects/${key}/members/${userId}`,
        method: "PUT",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Менеджер проекта или администратор (§2). Единственного активного менеджера — 409 LAST_MANAGER.
     *
     * @tags projects
     * @name V1ProjectsMembersDelete
     * @summary Исключить участника
     * @request DELETE:/api/v1/projects/{key}/members/{user_id}
     * @secure
     */
    v1ProjectsMembersDelete: (
      key: string,
      userId: string,
      params: RequestParams = {},
    ) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/projects/${key}/members/${userId}`,
        method: "DELETE",
        secure: true,
        ...params,
      }),

    /**
     * No description
     *
     * @tags projects
     * @name V1ProjectsRestoreCreate
     * @summary Восстановить проект
     * @request POST:/api/v1/projects/{key}/restore
     * @secure
     */
    v1ProjectsRestoreCreate: (key: string, params: RequestParams = {}) =>
      this.request<ProjectView, ApierrResponse>({
        path: `/api/v1/projects/${key}/restore`,
        method: "POST",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Участник проекта. Порядок: активный, запланированные по дате начала, завершённые (новые первыми). tasks_count и estimate_minutes — итог секции (§7.4), overdue — активный спринт после end_date (§5.3).
     *
     * @tags sprints
     * @name V1ProjectsSprintsList
     * @summary Спринты проекта
     * @request GET:/api/v1/projects/{key}/sprints
     * @secure
     */
    v1ProjectsSprintsList: (key: string, params: RequestParams = {}) =>
      this.request<SprintView[], ApierrResponse>({
        path: `/api/v1/projects/${key}/sprints`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Менеджер (§2, §5.1). Состояние planned; без названия — «Спринт N»; даты необязательны до запуска, end_date позже start_date и не дальше 42 дней.
     *
     * @tags sprints
     * @name V1ProjectsSprintsCreate
     * @summary Создать спринт
     * @request POST:/api/v1/projects/{key}/sprints
     * @secure
     */
    v1ProjectsSprintsCreate: (
      key: string,
      body: SprintRequest,
      params: RequestParams = {},
    ) =>
      this.request<SprintView, ApierrResponse>({
        path: `/api/v1/projects/${key}/sprints`,
        method: "POST",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Фильтры (повторяемые или через запятую): status, type, priority; assignee_id и sprint_id — UUID или null (без исполнителя / бэклог); label; q — поиск по ключу (с начала) и названию. include_deleted — только администратор. Порядок — ручной порядок секции (position), затем номер; limit до 200 (по умолчанию 50), следующая страница — cursor из next_cursor. overdue — срок прошёл по поясу проекта, а задача не закрыта.
     *
     * @tags tasks
     * @name V1ProjectsTasksList
     * @summary Задачи проекта
     * @request GET:/api/v1/projects/{key}/tasks
     * @secure
     */
    v1ProjectsTasksList: (
      key: string,
      query?: {
        /** статусы */
        status?: string[];
        /** типы */
        type?: string[];
        /** приоритеты */
        priority?: string[];
        /** исполнители; null — без исполнителя */
        assignee_id?: string[];
        /** спринты; null — бэклог */
        sprint_id?: string[];
        /** метка */
        label?: string;
        /** поиск по ключу и названию */
        q?: string;
        /** с удалёнными (администратор) */
        include_deleted?: boolean;
        /** до 200 */
        limit?: number;
        /** курсор следующей страницы */
        cursor?: string;
      },
      params: RequestParams = {},
    ) =>
      this.request<TaskPage, ApierrResponse>({
        path: `/api/v1/projects/${key}/tasks`,
        method: "GET",
        query: query,
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Любой участник проекта (§2). Номер — следующий в проекте, ключ вида TM-42, статус todo, автор — текущий пользователь. Исполнителя (активный участник) и спринт (не завершённый) при создании задаёт только менеджер или администратор. Поля и ограничения — §3.3. В архивном проекте — 409 PROJECT_ARCHIVED.
     *
     * @tags tasks
     * @name V1ProjectsTasksCreate
     * @summary Создать задачу
     * @request POST:/api/v1/projects/{key}/tasks
     * @secure
     */
    v1ProjectsTasksCreate: (
      key: string,
      body: TaskCreateRequest,
      params: RequestParams = {},
    ) =>
      this.request<TaskTask, ApierrResponse>({
        path: `/api/v1/projects/${key}/tasks`,
        method: "POST",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * No description
     *
     * @tags sprints
     * @name V1SprintsDetail
     * @summary Спринт
     * @request GET:/api/v1/sprints/{id}
     * @secure
     */
    v1SprintsDetail: (id: string, params: RequestParams = {}) =>
      this.request<SprintView, ApierrResponse>({
        path: `/api/v1/sprints/${id}`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Менеджер; только planned (§5.5), его задачи возвращаются в бэклог. Иначе — 409 INVALID_TRANSITION.
     *
     * @tags sprints
     * @name V1SprintsDelete
     * @summary Удалить спринт
     * @request DELETE:/api/v1/sprints/{id}
     * @secure
     */
    v1SprintsDelete: (id: string, params: RequestParams = {}) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/sprints/${id}`,
        method: "DELETE",
        secure: true,
        ...params,
      }),

    /**
     * @description Менеджер. Меняются только переданные поля; null очищает цель и даты (у активного даты обязательны). Завершённый спринт не изменяется — 409 INVALID_TRANSITION (details.reason = closed).
     *
     * @tags sprints
     * @name V1SprintsPartialUpdate
     * @summary Изменить спринт
     * @request PATCH:/api/v1/sprints/{id}
     * @secure
     */
    v1SprintsPartialUpdate: (
      id: string,
      body: SprintRequest,
      params: RequestParams = {},
    ) =>
      this.request<SprintView, ApierrResponse>({
        path: `/api/v1/sprints/${id}`,
        method: "PATCH",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Менеджер (§5.2). Под блокировкой проекта: в проекте нет активного (иначе 409 SPRINT_ALREADY_ACTIVE), даты заполнены, end позже start, не больше 42 дней, в спринте есть задачи (иначе 409 INVALID_TRANSITION, details.reason: dates | empty | not_planned). Фиксирует объём на старте: задачи, сумма оценок, сколько без оценки.
     *
     * @tags sprints
     * @name V1SprintsStartCreate
     * @summary Запустить спринт
     * @request POST:/api/v1/sprints/{id}/start
     * @secure
     */
    v1SprintsStartCreate: (id: string, params: RequestParams = {}) =>
      this.request<SprintStartResponse, ApierrResponse>({
        path: `/api/v1/sprints/${id}/start`,
        method: "POST",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * No description
     *
     * @tags tasks
     * @name V1TasksDetail
     * @summary Задача
     * @request GET:/api/v1/tasks/{task_key}
     * @secure
     */
    v1TasksDetail: (taskKey: string, params: RequestParams = {}) =>
      this.request<TaskTask, ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Мягкое удаление (§4): задача пропадает с доски, из бэклога, поиска и бота; записи времени остаются в отчётах. Менеджер проекта или администратор. Восстановление — POST /tasks/{key}/restore (администратор).
     *
     * @tags tasks
     * @name V1TasksDelete
     * @summary Удалить задачу
     * @request DELETE:/api/v1/tasks/{task_key}
     * @secure
     */
    v1TasksDelete: (taskKey: string, params: RequestParams = {}) =>
      this.request<void, ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}`,
        method: "DELETE",
        secure: true,
        ...params,
      }),

    /**
     * @description Меняются только переданные поля; null очищает необязательное поле; поле status — 400 (статус меняется через /transition). Заголовок If-Match — ETag карточки ("5"), список ETag или *; сравнение сильное, W/"5" — 400. Если задачу уже изменили — 409 VERSION_CONFLICT, в details.current актуальная задача. Права (§2): автор, исполнитель или менеджер; исполнитель (активный участник) и спринт (не завершённый) — только менеджер; задачу из завершённого спринта не переносят; оценку задачи, которая была в работе (в том числе потом отменённой), — только менеджер. Исполнителя нельзя менять в закрытой задаче и снимать с задачи в работе (§4).
     *
     * @tags tasks
     * @name V1TasksPartialUpdate
     * @summary Изменить задачу
     * @request PATCH:/api/v1/tasks/{task_key}
     * @secure
     */
    v1TasksPartialUpdate: (
      taskKey: string,
      body: TaskUpdateRequest,
      params: RequestParams = {},
    ) =>
      this.request<TaskTask, ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}`,
        method: "PATCH",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * No description
     *
     * @tags comments
     * @name V1TasksCommentsList
     * @summary Комментарии
     * @request GET:/api/v1/tasks/{task_key}/comments
     * @secure
     */
    v1TasksCommentsList: (taskKey: string, params: RequestParams = {}) =>
      this.request<TaskCommentView[], ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}/comments`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Любой участник проекта (§2). Markdown, 1–5000 символов (§3.6). Событие comment.created.
     *
     * @tags comments
     * @name V1TasksCommentsCreate
     * @summary Добавить комментарий
     * @request POST:/api/v1/tasks/{task_key}/comments
     * @secure
     */
    v1TasksCommentsCreate: (
      taskKey: string,
      body: TaskCommentRequest,
      params: RequestParams = {},
    ) =>
      this.request<TaskCommentView, ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}/comments`,
        method: "POST",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * No description
     *
     * @tags tasks
     * @name V1TasksHistoryList
     * @summary История задачи
     * @request GET:/api/v1/tasks/{task_key}/history
     * @secure
     */
    v1TasksHistoryList: (taskKey: string, params: RequestParams = {}) =>
      this.request<TaskHistoryView[], ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}/history`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description §7.4: sprint_id — секция (null — бэклог; не передан — та же секция), after_task_id — после какой задачи этой секции (null — в начало). Смена секции — только менеджер; порядок внутри секции — любой участник. В завершённый спринт и из него переносить нельзя. Перенос в активный спринт и из него пишется в журнал спринта.
     *
     * @tags tasks
     * @name V1TasksMoveCreate
     * @summary Переместить задачу
     * @request POST:/api/v1/tasks/{task_key}/move
     * @secure
     */
    v1TasksMoveCreate: (
      taskKey: string,
      body: TaskMoveRequest,
      params: RequestParams = {},
    ) =>
      this.request<TaskTask, ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}/move`,
        method: "POST",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Только администратор системы (§2). Не удалённая задача возвращается без изменений.
     *
     * @tags tasks
     * @name V1TasksRestoreCreate
     * @summary Восстановить задачу
     * @request POST:/api/v1/tasks/{task_key}/restore
     * @secure
     */
    v1TasksRestoreCreate: (taskKey: string, params: RequestParams = {}) =>
      this.request<TaskTask, ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}/restore`,
        method: "POST",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Назначение себя и переход todo→in_progress одной транзакцией с блокировкой строки задачи (§8.4): из одновременных нажатий проходит одно. Задача уже у другого — 409 TASK_ALREADY_TAKEN (details.assignee); закрытая — 409 INVALID_TRANSITION. Повторный запрос того же исполнителя — 200 без изменений.
     *
     * @tags tasks
     * @name V1TasksTakeCreate
     * @summary Взять в работу
     * @request POST:/api/v1/tasks/{task_key}/take
     * @secure
     */
    v1TasksTakeCreate: (taskKey: string, params: RequestParams = {}) =>
      this.request<TaskTask, ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}/take`,
        method: "POST",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Переходы и права — §4: todo→in_progress (исполнитель; любой участник, если исполнителя нет — он им становится; менеджер), in_progress→todo (исполнитель, менеджер; исполнитель очищается), in_progress→review (исполнитель, менеджер), review→in_progress (автор, менеджер; comment от 5 символов), review→done (автор, если он не исполнитель; менеджер), done→in_progress (менеджер; если исполнитель деактивирован — assignee_id; из закрытого спринта — в активный или бэклог), todo/in_progress/review→cancelled (менеджер; comment обязателен), cancelled→todo (менеджер; исполнитель и спринт очищаются). Иной переход — 409 INVALID_TRANSITION.
     *
     * @tags tasks
     * @name V1TasksTransitionCreate
     * @summary Сменить статус
     * @request POST:/api/v1/tasks/{task_key}/transition
     * @secure
     */
    v1TasksTransitionCreate: (
      taskKey: string,
      body: TaskTransitionRequest,
      params: RequestParams = {},
    ) =>
      this.request<TaskTask, ApierrResponse>({
        path: `/api/v1/tasks/${taskKey}/transition`,
        method: "POST",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Администратору — все пользователи целиком (user.User). Менеджеру проекта — только активные и только id, email, display_name (user.Brief) — для добавления в проект.
     *
     * @tags users
     * @name V1UsersList
     * @summary Пользователи
     * @request GET:/api/v1/users
     * @secure
     */
    v1UsersList: (params: RequestParams = {}) =>
      this.request<UserUser[], ApierrResponse>({
        path: `/api/v1/users`,
        method: "GET",
        secure: true,
        format: "json",
        ...params,
      }),

    /**
     * @description Пользователь создаётся без пароля, на email уходит письмо со ссылкой установки пароля (ТЗ §7.1). timezone по умолчанию Europe/Moscow.
     *
     * @tags users
     * @name V1UsersCreate
     * @summary Создать пользователя
     * @request POST:/api/v1/users
     * @secure
     */
    v1UsersCreate: (body: UsersCreateRequest, params: RequestParams = {}) =>
      this.request<UserUser, ApierrResponse>({
        path: `/api/v1/users`,
        method: "POST",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),

    /**
     * @description Меняются только переданные поля. Последнего активного администратора системы нельзя деактивировать или лишить флага (400, поле is_active / is_system_admin). is_active=false — деактивация (ТЗ §3.1): сессии отзываются, привязка Telegram снимается; если пользователь — единственный активный менеджер проекта, 409 LAST_MANAGER со списком проектов в details.projects.
     *
     * @tags users
     * @name V1UsersPartialUpdate
     * @summary Изменить пользователя
     * @request PATCH:/api/v1/users/{id}
     * @secure
     */
    v1UsersPartialUpdate: (
      id: string,
      body: UsersUpdateRequest,
      params: RequestParams = {},
    ) =>
      this.request<UserUser, ApierrResponse>({
        path: `/api/v1/users/${id}`,
        method: "PATCH",
        body: body,
        secure: true,
        type: ContentType.Json,
        format: "json",
        ...params,
      }),
  };
  health = {
    /**
     * @description Проверяет PostgreSQL и Redis. 200 — всё доступно, 503 — есть недоступный компонент.
     *
     * @tags system
     * @name HealthList
     * @summary Состояние сервиса
     * @request GET:/health
     */
    healthList: (params: RequestParams = {}) =>
      this.request<Record<string, any>, Record<string, any>>({
        path: `/health`,
        method: "GET",
        format: "json",
        ...params,
      }),
  };
}
