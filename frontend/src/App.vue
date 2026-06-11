<template>
  <div class="shell">
    <header class="topbar">
      <div>
        <p class="eyebrow">Server First Download</p>
        <h1>Lumara 图片取回台</h1>
      </div>
      <div class="top-actions">
        <nav class="page-tabs" aria-label="页面切换">
          <button
            class="nav-pill"
            :class="{ active: activePage === 'workspace' }"
            type="button"
            @click="activePage = 'workspace'"
          >
            下载任务
          </button>
          <button
            class="nav-pill"
            :class="{ active: activePage === 'guide' }"
            type="button"
            @click="activePage = 'guide'"
          >
            图片类型
          </button>
        </nav>
        <div class="status-strip">
          <span class="status-dot" :class="{ online: healthOk }"></span>
          <span>{{ healthOk ? '后端在线' : '连接中' }}</span>
        </div>
      </div>
    </header>

    <main v-if="activePage === 'workspace'" class="workspace">
      <section class="panel create-panel">
        <div class="section-title">
          <DownloadCloud :size="18" />
          <h2>新建下载任务</h2>
        </div>

        <form class="download-form compact-toolbar" @submit.prevent="startDownload">
          <label class="compact-field date-field">
            <span>开始日期</span>
            <input v-model="downloadForm.from" type="date" required />
          </label>
          <label class="compact-field date-field">
            <span>结束日期</span>
            <input v-model="downloadForm.to" type="date" required />
          </label>

          <div class="dropdown-field">
            <span class="field-label">分辨率</span>
            <button class="dropdown-trigger" type="button" @click="toggleDropdown('resolutions')">
              <span>{{ resolutionLabel }}</span>
              <ChevronDown :size="15" />
            </button>
            <div v-if="openDropdown === 'resolutions'" class="dropdown-menu compact-menu">
              <label v-for="resolution in options.resolutions" :key="resolution" class="menu-option">
                <input
                  type="checkbox"
                  :checked="downloadForm.resolutions.includes(resolution)"
                  @change="toggleArray(downloadForm.resolutions, resolution)"
                />
                <span>{{ resolution }}</span>
              </label>
            </div>
          </div>

          <div class="dropdown-field channel-field">
            <span class="field-label">图片类型</span>
            <button class="dropdown-trigger" type="button" @click="toggleDropdown('channels')">
              <span>{{ channelLabel }}</span>
              <ChevronDown :size="15" />
            </button>
            <div v-if="openDropdown === 'channels'" class="dropdown-menu channel-menu">
              <div class="menu-actions">
                <button type="button" class="text-button" @click="toggleAllChannels">
                  {{ allChannelsSelected ? '清空' : '全选' }}
                </button>
              </div>
              <label v-for="channel in options.channels" :key="channel.id" class="menu-option">
                <input
                  type="checkbox"
                  :checked="downloadForm.channels.includes(channel.id)"
                  @change="toggleArray(downloadForm.channels, channel.id)"
                />
                <span><strong>{{ channel.id }}</strong> {{ channel.title }}</span>
              </label>
            </div>
          </div>

          <label class="compact-field number-field">
            <span>数量限制</span>
            <input v-model.number="downloadForm.limit" min="0" type="number" />
          </label>
          <label class="compact-field number-field">
            <span>并发</span>
            <input v-model.number="downloadForm.workers" min="1" max="16" type="number" />
          </label>
          <label class="switch-line toolbar-check">
            <input v-model="downloadForm.verifyExisting" type="checkbox" />
            <span>校验已存在</span>
          </label>
          <button class="primary-action toolbar-action" :disabled="busy || !canStart" type="submit">
            <Play :size="17" />
            <span>{{ busy ? '提交中' : '开始下载' }}</span>
          </button>
        </form>
      </section>

      <section class="panel tasks-panel">
        <div class="section-title">
          <Activity :size="18" />
          <h2>下载任务</h2>
          <button class="icon-button" type="button" title="刷新任务" @click="loadJobs">
            <RefreshCw :size="17" />
          </button>
        </div>

        <div v-if="jobs.length === 0" class="empty-state">暂无下载任务</div>
        <div v-else class="task-list">
          <article
            v-for="job in jobs"
            :key="job.id"
            class="task-card"
            :class="{ active: job.id === activeJobId }"
            role="button"
            tabindex="0"
            @click="selectJob(job.id)"
            @keydown.enter.prevent="selectJob(job.id)"
            @keydown.space.prevent="selectJob(job.id)"
          >
            <span class="task-topline">
              <strong>{{ job.request.from }} → {{ job.request.to }}</strong>
              <span class="task-controls">
                <button
                  v-if="canDeleteJob(job)"
                  class="delete-task-button"
                  type="button"
                  title="删除任务记录"
                  @click.stop="deleteJob(job.id)"
                >
                  <Trash2 :size="15" />
                </button>
                <span class="badge" :class="job.status">{{ statusText(job.status) }}</span>
              </span>
            </span>
            <span class="task-subtitle">{{ job.request.resolutions.join(', ') }} / {{ job.request.channels.join(', ') }}</span>
            <span class="progress-track">
              <span class="progress-fill" :style="{ width: `${jobPercent(job)}%` }"></span>
            </span>
            <span class="task-meta">
              <span>{{ job.downloaded }} 下载</span>
              <span>{{ job.skipped }} 已有</span>
              <span>{{ job.failed }} 失败</span>
            </span>
            <span v-if="job.message" class="task-message">{{ job.message }}</span>
          </article>
        </div>
      </section>

      <section class="panel gallery-panel">
        <div class="gallery-head">
          <div class="section-title">
            <Images :size="18" />
            <h2>任务图片</h2>
          </div>
          <div class="gallery-buttons">
            <button class="secondary-action" type="button" :disabled="!activeJob" @click="loadImages">
              <RefreshCw :size="16" />
              <span>刷新</span>
            </button>
            <button
              class="secondary-action"
              type="button"
              :disabled="selectedPaths.length === 0"
              @click="downloadSelectedZip"
            >
              <Archive :size="16" />
              <span>下载已选</span>
            </button>
            <button
              class="primary-small"
              type="button"
              :disabled="!activeJob || imageResponse.total === 0 || archiveRunning"
              @click="downloadJobZip"
            >
              <DownloadCloud :size="16" />
              <span>{{ archiveRunning ? '正在压缩' : '下载整个任务目录' }}</span>
            </button>
          </div>
        </div>

        <div v-if="visibleArchiveJob" class="archive-progress">
          <div class="archive-progress-head">
            <strong>{{ visibleArchiveJob.message }}</strong>
            <span>{{ visibleArchiveJob.done }} / {{ visibleArchiveJob.total }} · {{ archivePercent }}%</span>
          </div>
          <div class="progress-track">
            <div class="progress-fill" :style="{ width: `${archivePercent}%` }"></div>
          </div>
        </div>

        <div v-if="!activeJob" class="empty-state large">先创建或选择一个下载任务</div>
        <div v-else-if="images.length === 0" class="empty-state large">这个任务暂时没有可展示的图片</div>
        <div v-else class="image-scroll">
          <div class="image-grid">
            <article v-for="image in images" :key="image.path" class="image-card">
              <button class="thumb-button" type="button" @click="openImage(image)">
                <img :src="image.thumbnailUrl" :alt="image.filename" loading="lazy" />
              </button>
              <div class="image-card-footer">
                <label class="image-check">
                  <input :checked="isSelected(image.path)" type="checkbox" @change="toggleSelected(image.path)" />
                  <span>{{ image.channel }} / {{ image.resolution }}</span>
                </label>
                <button class="icon-button" type="button" title="查看原图" @click="openImage(image)">
                  <Eye :size="16" />
                </button>
              </div>
              <p>{{ image.date }} {{ image.time }}</p>
            </article>
          </div>
        </div>

        <footer v-if="activeJob && imageResponse.total > 0" class="pager">
          <button class="secondary-action" type="button" :disabled="currentPage <= 1" @click="goPage(currentPage - 1)">
            <ChevronLeft :size="16" />
            <span>上一页</span>
          </button>
          <span>第 {{ currentPage }} / {{ totalPages }} 页</span>
          <button class="secondary-action" type="button" :disabled="currentPage >= totalPages" @click="goPage(currentPage + 1)">
            <span>下一页</span>
            <ChevronRight :size="16" />
          </button>
        </footer>
      </section>
    </main>

    <main v-else class="guide-page">
      <section class="panel guide-panel">
        <div class="guide-head">
          <div class="section-title">
            <Images :size="18" />
            <h2>图片类型</h2>
          </div>
          <p>这些类型对应 SDO 常用观测通道，示例图使用 512 分辨率的最新公开图像。</p>
        </div>

        <div class="guide-grid">
          <article v-for="item in channelGuide" :key="item.id" class="guide-card">
            <img class="guide-image" :src="item.image" :alt="`${item.id} ${item.name}`" loading="lazy" />
            <div class="guide-content">
              <div class="guide-title-row">
                <span class="guide-code">{{ item.id }}</span>
                <strong>{{ item.title }}</strong>
              </div>
              <span class="guide-name">{{ item.name }}</span>
              <p>{{ item.description }}</p>
            </div>
          </article>
        </div>
      </section>
    </main>

    <div v-if="activeImage" class="modal-backdrop" @click.self="activeImage = null">
      <section class="image-modal">
        <header>
          <div>
            <strong>{{ activeImage.filename }}</strong>
            <span>{{ activeImage.resolution }} / {{ activeImage.channelName }}</span>
          </div>
          <button class="icon-button" type="button" title="关闭" @click="activeImage = null">
            <X :size="18" />
          </button>
        </header>
        <img :src="activeImage.originalUrl" :alt="activeImage.filename" />
      </section>
    </div>

    <div v-if="toast" class="toast">{{ toast }}</div>
  </div>
</template>

<script setup>
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import {
  Activity,
  Archive,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  DownloadCloud,
  Eye,
  Images,
  Play,
  RefreshCw,
  Trash2,
  X
} from '@lucide/vue'

const today = new Date().toISOString().slice(0, 10)
const options = reactive({ resolutions: [], channels: [] })
const jobs = ref([])
const images = ref([])
const imageResponse = reactive({ total: 0, limit: 12, offset: 0 })
const selected = ref(new Set())
const activeJobId = ref('')
const activePage = ref('workspace')
const activeImage = ref(null)
const toast = ref('')
const busy = ref(false)
const healthOk = ref(false)
const openDropdown = ref('')
const archiveJob = ref(null)
const currentPage = ref(1)
const pageSize = 12

const downloadForm = reactive({
  from: today,
  to: today,
  resolutions: ['512'],
  channels: ['0304'],
  limit: 0,
  workers: 4,
  verifyExisting: false
})

const channelGuide = [
  {
    id: '0304',
    name: 'Chromosphere',
    title: '色球层',
    description: '常用来观察日珥、耀斑和太阳边缘活动。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0304.jpg'
  },
  {
    id: '0171',
    name: 'Quiet Corona',
    title: '宁静日冕',
    description: '适合观察较安静的日冕环结构。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0171.jpg'
  },
  {
    id: '0193',
    name: 'Corona',
    title: '日冕',
    description: '适合观察日冕洞、热等离子体和较大尺度结构。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0193.jpg'
  },
  {
    id: '0211',
    name: 'Active Regions',
    title: '活跃区',
    description: '适合观察高温活跃区和磁场活动区域。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0211.jpg'
  },
  {
    id: '0094',
    name: 'Hot Flares',
    title: '高温耀斑',
    description: '适合观察非常热的耀斑区域。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0094.jpg'
  },
  {
    id: '0131',
    name: 'Flare Plasma',
    title: '耀斑等离子体',
    description: '适合观察强耀斑和极高温等离子体。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0131.jpg'
  },
  {
    id: '0335',
    name: 'Active Corona',
    title: '活跃日冕',
    description: '适合观察活跃日冕区域和环状结构。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_0335.jpg'
  },
  {
    id: '1600',
    name: 'Transition',
    title: '过渡区',
    description: '观察光球层到日冕之间的过渡区域。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_1600.jpg'
  },
  {
    id: '1700',
    name: 'Surface',
    title: '太阳表面',
    description: '接近可见光表面的观测图像。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_1700.jpg'
  },
  {
    id: 'HMIIF',
    name: 'Sunspots',
    title: '太阳黑子',
    description: '白光强度图，适合查看太阳黑子和表面纹理。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_HMIIF.jpg'
  },
  {
    id: 'HMIBC',
    name: 'Magnetic Field',
    title: '磁场图',
    description: '显示太阳表面磁场正负极分布。',
    image: 'https://sdo.gsfc.nasa.gov/assets/img/latest/latest_512_HMIBC.jpg'
  }
]

const activeJob = computed(() => jobs.value.find((job) => job.id === activeJobId.value))
const totalPages = computed(() => Math.max(1, Math.ceil(imageResponse.total / pageSize)))
const selectedPaths = computed(() => Array.from(selected.value))
const visibleArchiveJob = computed(() => {
  if (!archiveJob.value || archiveJob.value.sourceJobId !== activeJobId.value) return null
  return archiveJob.value
})
const archiveRunning = computed(() => {
  return visibleArchiveJob.value?.status === 'queued' || visibleArchiveJob.value?.status === 'running'
})
const archivePercent = computed(() => {
  const job = visibleArchiveJob.value
  if (!job) return 0
  if (job.status === 'done') return 100
  if (!job.total) return 0
  return Math.min(100, Math.round((job.done / job.total) * 100))
})

const resolutionLabel = computed(() => {
  if (downloadForm.resolutions.length === 0) return '选择分辨率'
  if (downloadForm.resolutions.length === options.resolutions.length) return '全部分辨率'
  return downloadForm.resolutions.join(', ')
})

const channelLabel = computed(() => {
  if (downloadForm.channels.length === 0) return '选择类型'
  if (downloadForm.channels.length === options.channels.length) return '全部类型'
  if (downloadForm.channels.length === 1) {
    const channel = options.channels.find((item) => item.id === downloadForm.channels[0])
    return channel ? `${channel.id} ${channel.title}` : downloadForm.channels[0]
  }
  return `${downloadForm.channels.length} 个类型`
})

const allChannelsSelected = computed(() => {
  return options.channels.length > 0 && downloadForm.channels.length === options.channels.length
})

const canStart = computed(() => {
  return downloadForm.from && downloadForm.to && downloadForm.resolutions.length > 0 && downloadForm.channels.length > 0
})

async function requestJson(path, init) {
  const response = await fetch(path, {
    headers: { 'Content-Type': 'application/json', ...(init?.headers || {}) },
    ...init
  })
  const payload = await response.json().catch(() => ({}))
  if (!response.ok) {
    throw new Error(payload.error || '请求失败')
  }
  return payload
}

async function loadOptions() {
  const payload = await requestJson('/api/options')
  options.resolutions = payload.resolutions || []
  options.channels = payload.channels || []
}

async function checkHealth() {
  try {
    await requestJson('/api/health')
    healthOk.value = true
  } catch {
    healthOk.value = false
  }
}

async function startDownload() {
  busy.value = true
  openDropdown.value = ''
  try {
    const job = await requestJson('/api/downloads', {
      method: 'POST',
      body: JSON.stringify(downloadForm)
    })
    activeJobId.value = job.id
    currentPage.value = 1
    selected.value = new Set()
    showToast('任务已提交，服务器会先把图片下到本地目录')
    await loadJobs()
    await loadImages()
  } catch (error) {
    showToast(error.message)
  } finally {
    busy.value = false
  }
}

async function loadJobs() {
  const payload = await requestJson('/api/downloads')
  jobs.value = payload.items || []
  if (!activeJobId.value && jobs.value.length > 0) {
    activeJobId.value = jobs.value[0].id
  }
  if (activeJobId.value && !jobs.value.some((job) => job.id === activeJobId.value)) {
    activeJobId.value = jobs.value[0]?.id || ''
  }
}

async function selectJob(id) {
  if (activeJobId.value === id) return
  activeJobId.value = id
  currentPage.value = 1
  selected.value = new Set()
  await loadImages()
}

async function deleteJob(id) {
  const job = jobs.value.find((item) => item.id === id)
  if (!job || !canDeleteJob(job)) return
  try {
    await requestJson(`/api/downloads/${id}`, { method: 'DELETE' })
    showToast('任务记录已删除')
    if (activeJobId.value === id) {
      activeJobId.value = ''
      images.value = []
      imageResponse.total = 0
      selected.value = new Set()
    }
    await loadJobs()
    if (activeJobId.value) {
      await loadImages()
    }
  } catch (error) {
    showToast(error.message)
  }
}

async function loadImages() {
  if (!activeJobId.value) {
    images.value = []
    imageResponse.total = 0
    imageResponse.offset = 0
    return
  }
  const params = new URLSearchParams()
  params.set('limit', String(pageSize))
  params.set('offset', String((currentPage.value - 1) * pageSize))
  const payload = await requestJson(`/api/downloads/${activeJobId.value}/images?${params.toString()}`)
  images.value = payload.items || []
  imageResponse.total = payload.total || 0
  imageResponse.limit = payload.limit || pageSize
  imageResponse.offset = payload.offset || 0
  selected.value = new Set([...selected.value].filter((path) => images.value.some((image) => image.path === path)))
}

async function goPage(page) {
  currentPage.value = Math.min(Math.max(page, 1), totalPages.value)
  selected.value = new Set()
  await loadImages()
}

function toggleArray(target, value) {
  const index = target.indexOf(value)
  if (index >= 0) {
    target.splice(index, 1)
  } else {
    target.push(value)
  }
}

function toggleDropdown(name) {
  openDropdown.value = openDropdown.value === name ? '' : name
}

function toggleAllChannels() {
  if (allChannelsSelected.value) {
    downloadForm.channels.splice(0)
  } else {
    downloadForm.channels.splice(0, downloadForm.channels.length, ...options.channels.map((channel) => channel.id))
  }
}

function isSelected(path) {
  return selected.value.has(path)
}

function toggleSelected(path) {
  const next = new Set(selected.value)
  if (next.has(path)) {
    next.delete(path)
  } else {
    next.add(path)
  }
  selected.value = next
}

function openImage(image) {
  activeImage.value = image
}

async function downloadSelectedZip() {
  await downloadZip('/api/archive', { paths: selectedPaths.value, name: 'lumara-selected.zip' })
}

async function downloadJobZip() {
  if (!activeJobId.value) return
  if (archiveRunning.value) return
  try {
    const started = await requestJson(`/api/downloads/${activeJobId.value}/archive-jobs`, {
      method: 'POST',
      body: '{}'
    })
    archiveJob.value = started
    showToast('服务器开始创建压缩包')
    await pollArchiveJob(started.id)
  } catch (error) {
    showToast(error.message)
  }
}

async function pollArchiveJob(id) {
  while (true) {
    await delay(900)
    const snapshot = await requestJson(`/api/archive-jobs/${id}`)
    archiveJob.value = snapshot
    if (snapshot.status === 'done') {
      downloadPreparedArchive(snapshot)
      showToast('压缩完成，开始下载')
      return
    }
    if (snapshot.status === 'failed') {
      throw new Error(snapshot.error || '压缩失败')
    }
  }
}

function downloadPreparedArchive(snapshot) {
  const link = document.createElement('a')
  link.href = snapshot.downloadUrl
  link.download = snapshot.name || 'lumara-task.zip'
  link.click()
}

function delay(ms) {
  return new Promise((resolve) => window.setTimeout(resolve, ms))
}

async function downloadZip(path, body) {
  try {
    const response = await fetch(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    })
    if (!response.ok) {
      const payload = await response.json().catch(() => ({}))
      throw new Error(payload.error || '打包失败')
    }
    const blob = await response.blob()
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = body.name || 'lumara-task.zip'
    link.click()
    URL.revokeObjectURL(url)
  } catch (error) {
    showToast(error.message)
  }
}

function jobPercent(job) {
  const total = job.found || job.downloaded + job.skipped + job.failed
  if (!total) return job.status === 'done' ? 100 : 0
  return Math.min(100, Math.round(((job.downloaded + job.skipped + job.failed) / total) * 100))
}

function statusText(status) {
  return {
    queued: '排队',
    running: '运行',
    done: '完成',
    failed: '失败',
    canceled: '取消'
  }[status] || status
}

function canDeleteJob(job) {
  return job.status !== 'queued' && job.status !== 'running'
}

function showToast(message) {
  toast.value = message
  window.clearTimeout(showToast.timer)
  showToast.timer = window.setTimeout(() => {
    toast.value = ''
  }, 2600)
}

let timer
onMounted(async () => {
  await Promise.all([loadOptions(), checkHealth(), loadJobs()])
  await loadImages()
  timer = window.setInterval(async () => {
    await Promise.all([checkHealth(), loadJobs()])
    if (jobs.value.some((job) => job.status === 'running' || job.status === 'queued')) {
      await loadImages()
    }
  }, 2500)
})

watch(activeJobId, () => {
  if (currentPage.value > totalPages.value) {
    currentPage.value = totalPages.value
  }
})

onUnmounted(() => {
  window.clearInterval(timer)
})
</script>
