const assert = require('node:assert/strict')
const fs = require('node:fs')
const http = require('node:http')
const os = require('node:os')
const path = require('node:path')
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright')

// Run after npm run build. Every API is mocked, and external requests are blocked.
const dist = path.resolve(__dirname, '../dist')
const output = fs.mkdtempSync(path.join(os.tmpdir(), 'clicd-display-name-'))
const sharedName = 'shared-service.example.test'
const makeContainer = (id, name, extra = {}) => ({
  id, uuid: `00000000-0000-4000-8000-${String(id).padStart(12, '0')}`, name,
  virtualization: 'lxc', template: 'debian-bookworm', status: 'running',
  vcpu: 1, cpu_percent: 0, ram_mb: 512, swap_mb: 0, disk_gb: 5,
  network_bw_mbps: 100, network_down_mbps: 100, network_up_mbps: 100,
  monthly_traffic_gb: 100, traffic_mode: 'total', traffic_in_gb: 0, traffic_out_gb: 0,
  traffic_used_rx: 0, traffic_used_tx: 0, traffic_reset_date: '2026-11',
  io_speed_mbps: 0, io_read_mbps: 0, io_write_mbps: 0,
  ip: `10.0.0.${id}`, ipv6: '', public_ipv4s: [], ipv6_addresses: [],
  ssh_port: 22000 + id, ssh_password: 'fixture-password', ssh_auth_mode: 'password',
  port_mappings: [], port_mapping_limit: 2, ipv6_port_mappings: [], ipv6_port_mapping_limit: 0,
  snapshot_limit: 3, data_disks: [], created_at: '2026-11-01 00:00:00', expires_at: '',
  ...extra,
})
const containers = [
  makeContainer(1, 'whmcs-101-aaaaaaaaaaaa', { display_name: sharedName }),
  makeContainer(2, 'whmcs-102-bbbbbbbbbbbb', { display_name: sharedName }),
  makeContainer(3, 'legacy-raw-name'),
  makeContainer(4, 'empty-display-raw-name', { display_name: '' }),
]
const tasks = [
  { id: 'task-1', type: 'stop', status: 'done', container_id: 2, container_uuid: containers[1].uuid, container_name: containers[1].name, created_at: '2026-11-01 00:00:00' },
  { id: 'task-2', type: 'stop', status: 'done', container_id: 2, container_uuid: '99999999-9999-4999-8999-999999999999', container_name: containers[1].name, created_at: '2026-11-01 00:00:00' },
]
const requests = []
const errors = []
const checks = []
let browser
let page
let server

async function eventually(check, message) {
  const deadline = Date.now() + 10000
  do {
    if (await check()) return
    await new Promise(resolve => setTimeout(resolve, 75))
  } while (Date.now() < deadline)
  assert.fail(message)
}

async function step(name, run) {
  await run()
  checks.push(name)
  console.log(`PASS ${name}`)
}

async function main() {
  assert.ok(fs.existsSync(path.join(dist, 'index.html')), 'Build frontend/dist before this check')
  const servedDist = path.join(output, 'dist')
  fs.cpSync(dist, servedDist, { recursive: true })
  server = http.createServer((req, res) => {
    const requested = decodeURIComponent(new URL(req.url, 'http://localhost').pathname)
    let target = path.resolve(servedDist, `.${requested}`)
    if (!target.startsWith(servedDist + path.sep) || !fs.existsSync(target) || fs.statSync(target).isDirectory()) {
      target = path.join(servedDist, 'index.html')
    }
    const types = { '.html': 'text/html', '.js': 'application/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.png': 'image/png' }
    res.setHeader('Content-Type', types[path.extname(target)] || 'application/octet-stream')
    const stream = fs.createReadStream(target)
    stream.on('error', () => { res.statusCode = 500; res.end('Local build asset unavailable') })
    stream.pipe(res)
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  const origin = `http://127.0.0.1:${server.address().port}`
  browser = await chromium.launch({ headless: false, ...(process.env.PW_EXECUTABLE_PATH ? { executablePath: process.env.PW_EXECUTABLE_PATH } : {}) })
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 }, locale: 'zh-CN' })
  await context.addInitScript(() => {
    localStorage.setItem('clicd_token', 'fixture.' + btoa(JSON.stringify({ username: 'local-fixture' })) + '.fixture')
    localStorage.setItem('clicd_username', 'local-fixture')
  })
  await context.route('**/*', async route => {
    const request = route.request()
    const url = new URL(request.url())
    if (url.origin !== origin) return route.abort('blockedbyclient')
    if (!url.pathname.startsWith('/api/')) return route.continue()
    const endpoint = url.pathname.slice(4)
    const method = request.method()
    const body = request.postDataJSON()
    requests.push({ method, endpoint, body })
    let data = {}
    let status = 200
    if (endpoint === '/containers') data = containers
    else if (endpoint === '/tasks') data = tasks
    else if (endpoint === '/api-keys') data = []
    else if (endpoint === '/language') data = { language: 'zh' }
    else if (endpoint === '/version') data = { version: 'local-fixture' }
    else if (endpoint === '/host-info') data = { cpu: { cores: 8 }, ram: { total_mb: 8192 }, network: { public_ipv4_addresses: [] } }
    else if (endpoint === '/storage') data = { pools: [{ id: 'local', name: 'Local fixture', enabled: true, available: true, path: '/fixture', content_types: ['snapshots', 'backups', 'lxc'] }], disks: [] }
    else if (endpoint === '/ssh-ticket' || endpoint === '/vnc-ticket') status = 503
    else if (endpoint.startsWith('/containers/')) {
      const [, , identifier, action] = endpoint.split('/')
      const container = containers.find(item => item.uuid === identifier || String(item.id) === identifier || item.name === identifier)
      if (!container) { status = 404 }
      else if (!action) data = container
      else if (action === 'history') data = []
      else if (action === 'usage') data = { cpu_usage_pct: container.id * 10, memory_usage_bytes: container.id * 1024 * 1024, memory_total_bytes: 512 * 1024 * 1024, disk_usage_bytes: 1024, network_rx_bytes: 0, network_tx_bytes: 0, network_rx_rate: 0, network_tx_rate: 0 }
      else if (action === 'snapshots') data = { snapshots: [], quota: 3, schedule: null }
      else if (action === 'traffic') data = { traffic_mode: 'total', monthly_traffic_gb: 100, traffic_used_rx: 0, traffic_used_tx: 0 }
    }
    if (endpoint === '/api-keys' && method === 'POST') data = { id: 'fixture-key', ...body }
    await route.fulfill({ status, contentType: 'application/json', body: JSON.stringify({ success: status < 400, data, message: status >= 400 ? 'Local test deliberately stops before connection' : '' }) })
  })
  page = await context.newPage()
  page.on('pageerror', error => errors.push(error.message))
  const rows = () => page.getByRole('row').filter({ has: page.getByRole('button', { name: '查看', exact: true }) })
  const sharedRows = () => rows().filter({ hasText: sharedName })

  await step('duplicate display labels keep two instances; legacy and empty labels fall back', async () => {
    await page.goto(`${origin}/containers`)
    await eventually(async () => await rows().count() === 4, 'Expected four instance rows')
    assert.equal(await sharedRows().count(), 2)
    await rows().filter({ hasText: 'legacy-raw-name' }).waitFor()
    await rows().filter({ hasText: 'empty-display-raw-name' }).waitFor()
    await page.screenshot({ path: path.join(output, 'containers.png'), fullPage: true })
  })
  await step('search supports both display name and original name', async () => {
    const search = page.getByPlaceholder(/搜索/)
    await search.fill(sharedName)
    await eventually(async () => await rows().count() === 2, 'Display-name search should retain both duplicate labels')
    await search.fill(containers[1].name)
    await eventually(async () => await rows().count() === 1, 'Original name must remain searchable')
    await sharedRows().waitFor()
    await search.fill('')
    await eventually(async () => await rows().count() === 4, 'Clearing search restores all instances')
  })
  await step('task queue resolves a current task display label by UUID', async () => {
    await page.getByRole('button', { name: '任务队列', exact: true }).click()
    const queue = page.getByRole('dialog', { name: '任务队列', exact: true })
    await queue.getByRole('cell', { name: sharedName, exact: true }).waitFor()
    assert.equal(await queue.getByRole('row').filter({ hasText: sharedName }).count(), 1)
  })
  await step('historical task with unknown UUID keeps original name despite reused name and numeric ID', async () => {
    const queue = page.getByRole('dialog', { name: '任务队列', exact: true })
    await queue.getByRole('cell', { name: containers[1].name, exact: true }).waitFor()
    assert.equal(await queue.getByRole('row').filter({ hasText: containers[1].name }).count(), 1)
    await queue.getByRole('button', { name: '关闭', exact: true }).click()
    assert.equal(tasks[0].container_name, containers[1].name)
    assert.equal(tasks[1].container_uuid, '99999999-9999-4999-8999-999999999999')
  })
  await step('second duplicate row opens its own UUID and usage requests remain distinct', async () => {
    await sharedRows().nth(1).getByRole('button', { name: '查看', exact: true }).click()
    await page.waitForURL(`${origin}/container/${containers[1].uuid}`)
    await page.getByRole('heading', { name: sharedName, exact: true }).waitFor()
    for (const item of containers.slice(0, 2)) {
      assert.ok(requests.some(request => request.endpoint === `/containers/${item.uuid}/usage`), `Missing UUID usage for ${item.name}`)
    }
  })
  await step('delete confirmation displays the label and cancel sends no delete', async () => {
    await page.getByRole('button', { name: '删除', exact: true }).click()
    const dialog = page.getByRole('alertdialog')
    await dialog.waitFor()
    assert.ok((await dialog.innerText()).includes(sharedName))
    assert.ok(!(await dialog.innerText()).includes(containers[1].name))
    await dialog.getByRole('button', { name: '取消', exact: true }).click()
    await dialog.waitFor({ state: 'hidden' })
    assert.equal(requests.filter(request => request.method === 'DELETE').length, 0)
  })
  await step('snapshot confirmation displays the label and cancel sends no snapshot write', async () => {
    await page.getByRole('button', { name: '快照', exact: true }).click()
    const snapshots = page.getByRole('dialog', { name: '快照', exact: true })
    await snapshots.getByRole('button', { name: '新建快照', exact: true }).click()
    const confirm = page.getByRole('alertdialog')
    await confirm.waitFor()
    assert.ok((await confirm.innerText()).includes(sharedName))
    await confirm.getByRole('button', { name: '取消', exact: true }).click()
    await confirm.waitFor({ state: 'hidden' })
    assert.equal(requests.filter(request => request.method === 'POST' && request.endpoint.includes('/snapshots')).length, 0)
    await snapshots.getByRole('button', { name: '关闭', exact: true }).click()
  })
  await step('SSH displays the label while ticket uses original name', async () => {
    await page.getByRole('button', { name: 'WebSSH', exact: true }).click()
    const ssh = page.getByRole('dialog', { name: `WebSSH - ${sharedName}`, exact: true })
    await ssh.waitFor()
    await eventually(() => requests.some(request => request.endpoint === '/ssh-ticket'), 'Expected SSH ticket request')
    assert.equal(requests.find(request => request.endpoint === '/ssh-ticket').body.container_name, containers[1].name)
    await ssh.getByRole('button', { name: '关闭', exact: true }).last().click()
  })
  await step('transfer modal labels the source while clone name still derives from original name', async () => {
    await page.getByRole('button', { name: '备份、克隆与模板', exact: true }).click()
    const transfer = page.getByRole('dialog', { name: '备份、克隆与模板', exact: true })
    await transfer.waitFor()
    assert.ok((await transfer.innerText()).includes(sharedName))
    await transfer.getByRole('button', { name: '完整克隆', exact: true }).click()
    assert.equal(await transfer.getByLabel('新实例名称', { exact: true }).inputValue(), `${containers[1].name}-copy`)
    await transfer.getByRole('button', { name: '取消', exact: true }).click()
  })
  await step('API permission labels show display name but selected payload remains UUID', async () => {
    await page.goto(`${origin}/api-integration`)
    await page.getByRole('button', { name: '创建 Key', exact: true }).click()
    const form = page.getByRole('dialog', { name: '创建 API Key', exact: true })
    await form.getByPlaceholder('CI/CD、计费系统、自动化脚本').fill('Display-name fixture')
    const permissionLabels = form.locator('label').filter({ hasText: sharedName })
    assert.equal(await permissionLabels.count(), 2)
    await permissionLabels.nth(1).getByRole('checkbox').click()
    const posted = page.waitForRequest(request => new URL(request.url()).pathname === '/api/api-keys' && request.method() === 'POST')
    await form.getByRole('button', { name: '保存', exact: true }).click()
    assert.deepEqual((await posted).postDataJSON().container_uuids, [containers[1].uuid])
    await form.waitFor({ state: 'hidden' })
    await page.screenshot({ path: path.join(output, 'api-permissions.png'), fullPage: true })
  })
  assert.deepEqual(errors, [], 'Browser must not raise application errors')
  const writes = requests.filter(request => !['GET', 'HEAD'].includes(request.method))
  assert.deepEqual(writes.map(request => request.endpoint).sort(), ['/api-keys', '/ssh-ticket'])
  return { success: true, checks, requests, errors, output }
}

main().then(result => fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify(result, null, 2))).catch(async error => {
  if (page) await page.screenshot({ path: path.join(output, 'failure.png'), fullPage: true }).catch(() => {})
  fs.writeFileSync(path.join(output, 'report.json'), JSON.stringify({ success: false, checks, error: error.stack, errors, requests, output }, null, 2))
  console.error(error)
  process.exitCode = 1
}).finally(async () => {
  if (browser) await browser.close()
  if (server) await new Promise(resolve => server.close(resolve))
  console.log(`Artifacts: ${output}`)
})
