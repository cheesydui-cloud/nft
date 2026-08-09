/* REALITY dest / SNI candidates. Pick by node region; always verify TLS1.3+h2+X25519 on the node before publish. */
export const REALITY_DOMAIN_POOL = [
  // 香港
  { domain: 'www.ust.hk', region: 'hk', label: '高校 · 科技大学', flag: '🇭🇰' },
  { domain: 'www.cityu.edu.hk', region: 'hk', label: '高校 · 城市大学', flag: '🇭🇰' },
  { domain: 'www.mtr.com.hk', region: 'hk', label: '交通 · 港铁', flag: '🇭🇰' },
  // 日本
  { domain: 'www.kyoto-u.ac.jp', region: 'jp', label: '高校 · 京都大学', flag: '🇯🇵' },
  { domain: 'www.osaka-u.ac.jp', region: 'jp', label: '高校 · 大阪大学', flag: '🇯🇵' },
  { domain: 'www.waseda.jp', region: 'jp', label: '高校 · 早稻田大学', flag: '🇯🇵' },
  { domain: 'www.jreast.co.jp', region: 'jp', label: '交通 · JR 东日本', flag: '🇯🇵' },
  // 台湾
  { domain: 'www.ncku.edu.tw', region: 'tw', label: '高校 · 成功大学', flag: '🇹🇼' },
  { domain: 'www.tku.edu.tw', region: 'tw', label: '高校 · 淡江大学', flag: '🇹🇼' },
  { domain: 'www.tsmc.com', region: 'tw', label: '企业 · 台积电', flag: '🇹🇼' },
  // 新加坡
  { domain: 'www.nus.edu.sg', region: 'sg', label: '高校 · 国立大学', flag: '🇸🇬' },
  { domain: 'www.smu.edu.sg', region: 'sg', label: '高校 · 管理大学', flag: '🇸🇬' },
  { domain: 'www.changiairport.com', region: 'sg', label: '交通 · 樟宜机场', flag: '🇸🇬' },
  // 美国
  { domain: 'www.stanford.edu', region: 'us', label: '高校 · 斯坦福大学', flag: '🇺🇸' },
  { domain: 'www.berkeley.edu', region: 'us', label: '高校 · 加州大学伯克利', flag: '🇺🇸' },
  { domain: 'www.caltech.edu', region: 'us', label: '高校 · 加州理工', flag: '🇺🇸' },
  // 德国
  { domain: 'www.kit.edu', region: 'de', label: '高校 · 卡尔斯鲁厄理工', flag: '🇩🇪' },
  { domain: 'www.tu-berlin.de', region: 'de', label: '高校 · 柏林工业大学', flag: '🇩🇪' },
  { domain: 'www.hetzner.com', region: 'de', label: '主机商 · Hetzner', flag: '🇩🇪' },
  // 全球常见
  { domain: 'www.nvidia.com', region: 'global', label: '巨头 · NVIDIA', flag: '🌐' },
  { domain: 'www.sap.com', region: 'global', label: '巨头 · SAP', flag: '🌐' },
  { domain: 'www.adobe.com', region: 'global', label: '巨头 · Adobe', flag: '🌐' },
  { domain: 'www.microsoft.com', region: 'global', label: '巨头 · Microsoft', flag: '🌐' },
  { domain: 'www.apple.com', region: 'global', label: '巨头 · Apple', flag: '🌐' },
  { domain: 'www.cloudflare.com', region: 'global', label: 'CDN · Cloudflare', flag: '🌐' },
]

export const REALITY_FP_OPTIONS = [
  'chrome', 'firefox', 'edge', 'safari', '360', 'qq', 'ios', 'android', 'random', 'randomized',
]

export const REALITY_NETWORK_OPTIONS = [
  { value: 'tcp', label: 'tcp（裸 TCP · 推荐）' },
  { value: 'ws', label: 'ws（WebSocket）' },
  { value: 'httpupgrade', label: 'httpupgrade' },
  { value: 'xhttp', label: 'xhttp（仅 xray）' },
]
