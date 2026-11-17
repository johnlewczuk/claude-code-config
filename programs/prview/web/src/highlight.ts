// Syntax colouring, one line at a time. Multi-line strings and comments
// won't colour across lines; that's accepted for speed and simplicity.
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import c from 'highlight.js/lib/languages/c'
import cmake from 'highlight.js/lib/languages/cmake'
import cpp from 'highlight.js/lib/languages/cpp'
import css from 'highlight.js/lib/languages/css'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import go from 'highlight.js/lib/languages/go'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import lua from 'highlight.js/lib/languages/lua'
import makefile from 'highlight.js/lib/languages/makefile'
import markdown from 'highlight.js/lib/languages/markdown'
import python from 'highlight.js/lib/languages/python'
import rust from 'highlight.js/lib/languages/rust'
import sql from 'highlight.js/lib/languages/sql'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

const langs: Record<string, any> = {
  bash, c, cmake, cpp, css, dockerfile, go, java, javascript, json, lua, makefile, markdown, python, rust, sql,
  typescript, xml, yaml,
}
for (const [name, lang] of Object.entries(langs)) hljs.registerLanguage(name, lang)

const byExt: Record<string, string> = {
  sh: 'bash', bash: 'bash', zsh: 'bash',
  c: 'c', h: 'cpp', cc: 'cpp', cpp: 'cpp', cxx: 'cpp', hpp: 'cpp', hh: 'cpp', ino: 'cpp',
  cmake: 'cmake', css: 'css', go: 'go', java: 'java', kt: 'java',
  js: 'javascript', mjs: 'javascript', cjs: 'javascript', jsx: 'javascript',
  ts: 'typescript', tsx: 'typescript', json: 'json', lua: 'lua', mk: 'makefile',
  md: 'markdown', py: 'python', rs: 'rust', sql: 'sql',
  html: 'xml', xml: 'xml', svg: 'xml', yaml: 'yaml', yml: 'yaml',
}

export function languageFor(path: string): string | null {
  const base = path.split('/').pop() ?? ''
  if (base === 'CMakeLists.txt') return 'cmake'
  if (base === 'Makefile' || base === 'makefile') return 'makefile'
  if (base.startsWith('Dockerfile')) return 'dockerfile'
  const ext = base.includes('.') ? base.split('.').pop()!.toLowerCase() : ''
  return byExt[ext] ?? null
}

const cache = new Map<string, string>()

function escape(s: string) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

/** Returns HTML for one line of code. */
export function highlightLine(text: string, lang: string | null): string {
  if (!lang || text.length > 2000) return escape(text)
  const key = lang + '\0' + text
  let html = cache.get(key)
  if (html === undefined) {
    try {
      html = hljs.highlight(text, { language: lang, ignoreIllegals: true }).value
    } catch {
      html = escape(text)
    }
    if (cache.size > 50000) cache.clear()
    cache.set(key, html)
  }
  return html
}
