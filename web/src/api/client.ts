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
     * @description Проекты, где пользователь участник; администратору системы — все. По умолчанию — не архивные; archived=true — только архив; deleted=true — только удалённые (администратор).
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
      this.request<ProjectView[], ApierrResponse>({
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
