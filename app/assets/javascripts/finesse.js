// <finesse-stream-source src="…"> replaces <turbo-stream-source> for Finesse.
// Per src, one tab holds a Web Lock, opens the EventSource, and relays each
// message to the other tabs over a BroadcastChannel. When the leader goes
// away another tab takes the lock and resumes from the last id it saw.
// Without Web Locks or BroadcastChannel every element opens its own stream.
import { Turbo } from "@hotwired/turbo-rails"

const shared = "locks" in navigator && "BroadcastChannel" in window

class FinesseStreamSource extends HTMLElement {
  #active = false
  #lastId = ""
  #source = null
  #channel = null
  #abort = null
  #release = null

  connectedCallback() {
    window.addEventListener("pagehide", this.#pagehide)
    window.addEventListener("pageshow", this.#pageshow)
    this.#join()
  }

  disconnectedCallback() {
    window.removeEventListener("pagehide", this.#pagehide)
    window.removeEventListener("pageshow", this.#pageshow)
    this.#leave()
  }

  get src() {
    return this.getAttribute("src") || ""
  }

  // An open EventSource keeps a page out of the back/forward cache.
  #pagehide = () => this.#leave()
  #pageshow = (event) => { if (event.persisted) this.#join() }

  #join() {
    if (this.#active) return
    this.#active = true
    if (!shared) return this.#open()

    const name = `finesse:${this.src}`
    const abort = (this.#abort = new AbortController())
    this.#channel = new BroadcastChannel(name)
    this.#channel.onmessage = ({ data }) => this.#render(data)
    navigator.locks
      .request(name, { signal: abort.signal }, () => {
        if (abort.signal.aborted) return
        return new Promise((resolve) => {
          this.#release = resolve
          this.#open()
        })
      })
      .catch(() => {}) // AbortError: left while still waiting for the lock
  }

  #leave() {
    if (!this.#active) return
    this.#active = false
    this.#abort?.abort()
    this.#release?.()
    this.#source?.close()
    this.#channel?.close()
    this.#abort = this.#release = this.#source = this.#channel = null
  }

  #open() {
    const url = new URL(this.src, location.href)
    if (this.#lastId) url.searchParams.set("last_event_id", this.#lastId)
    const source = (this.#source = new EventSource(url))
    source.onmessage = ({ lastEventId, data }) => {
      const message = { id: lastEventId, data }
      this.#channel?.postMessage(message)
      this.#render(message)
    }
    // A closed source (the server refused it) won't retry; free the lock so
    // another tab can try.
    source.onerror = () => { if (source.readyState === EventSource.CLOSED) this.#release?.() }
  }

  #render({ id, data }) {
    if (id) this.#lastId = id
    Turbo.renderStreamMessage(data)
  }
}

if (!customElements.get("finesse-stream-source")) {
  customElements.define("finesse-stream-source", FinesseStreamSource)
}
