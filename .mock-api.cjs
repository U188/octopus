const http = require('http');

const now = new Date().toISOString();

const modelNames = [
  'gpt-4o',
  'gpt-4o-mini',
  'gpt-4.1',
  'gpt-4.1-mini',
  'gpt-5-mini',
  'claude-3.5-sonnet',
  'claude-3.7-sonnet',
  'claude-opus-4',
  'gemini-2.0-flash',
  'gemini-2.5-pro',
  'deepseek-chat',
  'deepseek-reasoner',
  'qwen-plus',
  'qwen-max',
  'doubao-seed-1.6',
  'moonshot-v1-32k',
  'glm-4-plus',
  'yi-large',
];

const channels = [
  { channel_id: 1, channel_name: 'OpenAI 主通道', site_name: 'OpenAI', site_account_name: 'primary', site_group_name: 'default', endpoint_type: 'OpenAI' },
  { channel_id: 2, channel_name: 'Claude 备用通道', site_name: 'Anthropic', site_account_name: 'backup', site_group_name: 'vip', endpoint_type: 'Anthropic' },
  { channel_id: 3, channel_name: 'Gemini 测试通道', site_name: 'Google', site_account_name: 'lab', site_group_name: 'flash', endpoint_type: 'Gemini' },
];

const modelChannels = channels.flatMap((channel, channelIndex) =>
  modelNames.map((name, modelIndex) => ({
    ...channel,
    name,
    enabled: true,
    site_id: channelIndex + 1,
    site_account_id: channelIndex + 10,
    site_group_key: channel.site_group_name,
  }))
);

let group = {
  id: 1,
  name: 'mobile-preview',
  mode: 1,
  match_regex: '',
  first_token_time_out: 0,
  session_keep_time: 0,
  retry_enabled: false,
  max_retries: 3,
  pinned: true,
  pinned_at: now,
  active_preset_id: null,
  items: [
    { id: 101, group_id: 1, channel_id: 1, model_name: 'gpt-4o', priority: 1, weight: 1 },
    { id: 102, group_id: 1, channel_id: 2, model_name: 'claude-3.5-sonnet', priority: 2, weight: 1 },
    { id: 103, group_id: 1, channel_id: 3, model_name: 'gemini-2.0-flash', priority: 3, weight: 1 },
  ],
};

const site = {
  id: 1,
  name: 'Preview API Site',
  platform: 'api',
  base_url: 'https://api.example.com/v1',
  enabled: true,
  proxy_mode: 'direct',
  proxy_config_id: null,
  external_checkin_url: null,
  is_pinned: true,
  sort_order: 0,
  global_weight: 1,
  custom_header: [],
  route_base_urls: [],
  tags: ['preview'],
  default_route_type: 'openai_chat',
  archived: false,
  archived_at: null,
  accounts: [
    {
      id: 1,
      site_id: 1,
      name: 'Main API Key Account',
      credential_type: 'api_key',
      username: '',
      password: '',
      access_token: '',
      api_key: 'sk-preview',
      refresh_token: '',
      token_expires_at: 0,
      platform_user_id: null,
      proxy_mode: 'inherit',
      proxy_config_id: null,
      enabled: true,
      auto_sync: true,
      auto_checkin: false,
      random_checkin: false,
      checkin_interval_hours: 24,
      checkin_random_window_minutes: 120,
      next_auto_checkin_at: null,
      last_sync_at: now,
      last_checkin_at: null,
      last_sync_status: 'success',
      last_checkin_status: 'idle',
      last_sync_message: 'mock sync ok',
      last_checkin_message: '',
      balance: 128.5,
      balance_used: 12.3,
      today_income: 1.25,
      tokens: [
        {
          id: 1,
          site_account_id: 1,
          name: 'default-key',
          token: 'sk-preview',
          value_status: 'ready',
          group_key: 'default',
          group_name: 'default',
          enabled: true,
          source: 'manual',
          is_default: true,
          last_sync_at: now,
        },
      ],
      user_groups: [
        { id: 1, site_account_id: 1, group_key: 'default', name: 'default' },
      ],
      models: modelNames.slice(0, 10).map((name, index) => ({
        id: index + 1,
        site_account_id: 1,
        group_key: 'default',
        model_name: name,
        source: 'sync',
        route_type: name.startsWith('claude') ? 'anthropic' : 'openai_chat',
        disabled: false,
      })),
      channel_bindings: [],
    },
  ],
};

let apiKeys = [
  {
    id: 1,
    name: 'preview-key',
    api_key: 'sk-preview-custom',
    enabled: true,
    expire_at: 0,
    max_cost: 0,
    max_rpm: 0,
    supported_models: '',
  },
];
let nextAPIKeyId = 2;

function send(res, status, data) {
  res.writeHead(status, {
    'Content-Type': 'application/json; charset=utf-8',
    'Access-Control-Allow-Origin': '*',
    'Access-Control-Allow-Headers': 'Content-Type, Authorization',
    'Access-Control-Allow-Methods': 'GET, POST, PUT, PATCH, DELETE, OPTIONS',
  });
  res.end(JSON.stringify(data));
}

function ok(res, data) {
  send(res, 200, { code: 0, message: 'success', data });
}

function readBody(req) {
  return new Promise((resolve) => {
    let raw = '';
    req.on('data', (chunk) => { raw += chunk; });
    req.on('end', () => {
      try {
        resolve(raw ? JSON.parse(raw) : {});
      } catch {
        resolve({});
      }
    });
  });
}

function applyGroupUpdate(body) {
  group = {
    ...group,
    ...(body.name !== undefined ? { name: body.name } : null),
    ...(body.mode !== undefined ? { mode: body.mode } : null),
    ...(body.match_regex !== undefined ? { match_regex: body.match_regex } : null),
    ...(body.first_token_time_out !== undefined ? { first_token_time_out: body.first_token_time_out } : null),
    ...(body.session_keep_time !== undefined ? { session_keep_time: body.session_keep_time } : null),
    ...(body.retry_enabled !== undefined ? { retry_enabled: body.retry_enabled } : null),
    ...(body.max_retries !== undefined ? { max_retries: body.max_retries } : null),
  };

  const deleteIds = new Set(body.items_to_delete || []);
  let items = (group.items || []).filter((item) => !deleteIds.has(item.id));

  for (const update of body.items_to_update || []) {
    items = items.map((item) => item.id === update.id ? { ...item, priority: update.priority, weight: update.weight } : item);
  }

  let nextId = Math.max(0, ...items.map((item) => item.id || 0)) + 1;
  for (const add of body.items_to_add || []) {
    items.push({ id: nextId++, group_id: group.id, ...add });
  }

  group.items = items.sort((a, b) => a.priority - b.priority);
}

function normalizeAPIKey(body, existing) {
  return {
    id: existing?.id ?? nextAPIKeyId++,
    name: body.name?.trim() || existing?.name || 'API Key',
    api_key: existing?.api_key ?? (body.api_key?.trim() || `sk-mock-${Date.now()}`),
    enabled: body.enabled ?? existing?.enabled ?? true,
    expire_at: body.expire_at ?? existing?.expire_at ?? 0,
    max_cost: body.max_cost ?? existing?.max_cost ?? 0,
    max_rpm: body.max_rpm ?? existing?.max_rpm ?? 0,
    supported_models: body.supported_models ?? existing?.supported_models ?? '',
  };
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, 'http://127.0.0.1:8080');

  if (req.method === 'OPTIONS') {
    send(res, 204, null);
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/v1/user/login') {
    const body = await readBody(req);
    if (body.username === 'admin' && body.password === 'admin') {
      ok(res, { token: 'mock-token', expire_at: new Date(Date.now() + 86400000).toISOString() });
    } else {
      send(res, 401, { message: 'invalid credentials', error_code: 'auth.invalid_credentials' });
    }
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/v1/user/status') {
    ok(res, 'ok');
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/v1/apikey/list') {
    ok(res, apiKeys);
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/v1/apikey/create') {
    const body = await readBody(req);
    const name = body.name?.trim();
    if (!name) {
      send(res, 400, { message: 'name is required', error_code: 'common.invalid_param' });
      return;
    }
    if (apiKeys.some((item) => item.name.trim().toLowerCase() === name.toLowerCase())) {
      send(res, 409, { message: 'name already exists', error_code: 'common.invalid_param' });
      return;
    }
    const customKey = body.api_key?.trim();
    if (customKey && apiKeys.some((item) => item.api_key === customKey)) {
      send(res, 409, { message: 'api_key already exists', error_code: 'common.invalid_param' });
      return;
    }
    const created = normalizeAPIKey({ ...body, name, api_key: customKey }, null);
    apiKeys.push(created);
    ok(res, created);
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/v1/apikey/update') {
    const body = await readBody(req);
    const index = apiKeys.findIndex((item) => item.id === body.id);
    if (index < 0) {
      send(res, 404, { message: 'API key not found' });
      return;
    }
    const name = body.name?.trim();
    if (!name) {
      send(res, 400, { message: 'name is required', error_code: 'common.invalid_param' });
      return;
    }
    if (apiKeys.some((item) => item.id !== body.id && item.name.trim().toLowerCase() === name.toLowerCase())) {
      send(res, 409, { message: 'name already exists', error_code: 'common.invalid_param' });
      return;
    }
    apiKeys[index] = normalizeAPIKey({ ...body, name }, apiKeys[index]);
    ok(res, apiKeys[index]);
    return;
  }

  if (req.method === 'DELETE' && url.pathname.startsWith('/api/v1/apikey/delete/')) {
    const id = Number(url.pathname.split('/').pop());
    apiKeys = apiKeys.filter((item) => item.id !== id);
    ok(res, null);
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/v1/group/list') {
    ok(res, [group]);
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/v1/site/list') {
    ok(res, [site]);
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/v1/site/account/test-conversation') {
    const body = await readBody(req);
    ok(res, {
      model: body.model,
      mode: body.mode,
      greeting: body.greeting || 'hi',
      reply: `Mock reply for "${body.greeting || 'hi'}" from ${body.model} (${body.mode}).`,
      duration_ms: 42,
      raw: { mock: true },
    });
    return;
  }

  if (req.method === 'POST' && url.pathname === '/api/v1/group/update') {
    applyGroupUpdate(await readBody(req));
    ok(res, group);
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/v1/model/channel') {
    ok(res, modelChannels);
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/v1/setting/list') {
    ok(res, [{ key: 'group_health_enabled', value: 'false' }]);
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/v1/group/health/list') {
    ok(res, []);
    return;
  }

  if (req.method === 'GET' && url.pathname === '/api/v1/stats/total') {
    ok(res, {});
    return;
  }

  ok(res, []);
});

server.listen(8080, '127.0.0.1', () => {
  console.log('mock api ready: http://127.0.0.1:8080');
  console.log('login: admin / admin');
});
