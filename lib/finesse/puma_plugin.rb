# frozen_string_literal: true

require_relative '../finesse'

module Finesse
  # Pure assembly of the child process env and args from the environment,
  # kept apart from the lifecycle class so both stay small and testable.
  module PumaPluginConfig
    DEFAULT_DEV_ORIGIN = 'http://localhost:3000'

    module_function

    # Returns { env:, args: } for the child process, or nil (after logging
    # why) when the SSE server should not start.
    def build_config(env = ENV, log: ->(msg) { warn msg })
      key = resolve_signing_key(env, log)
      return nil unless key

      origins = allowed_origins(env)
      if origins.empty?
        log.call('finesse: FINESSE_ALLOW_ORIGIN is not set outside development; not starting SSE server')
        return nil
      end
      { env: { 'FINESSE_SIGNING_KEY' => key }, args: build_args(env, origins) }
    end

    def resolve_signing_key(env, log)
      key = env['FINESSE_SIGNING_KEY']
      return key unless key.to_s.empty?

      Finesse.signing_key
    rescue StandardError, LoadError => e
      log.call("finesse: could not derive signing key (#{e.message}); not starting SSE server")
      nil
    end

    def allowed_origins(env)
      raw = env['FINESSE_ALLOW_ORIGIN'].to_s
      return raw.split(',').map(&:strip).reject(&:empty?) unless raw.empty?

      rails_env = env['RAILS_ENV'] || env['RACK_ENV']
      rails_env.to_s.empty? || rails_env == 'development' ? [DEFAULT_DEV_ORIGIN] : []
    end

    def build_args(env, origins)
      args = Platforms.cable_db_args
      origins.each { |origin| args.push('--allow-origin', origin) }
      port = env['FINESSE_PORT'].to_s
      args.push('--port', port) unless port.empty?
      args
    end
  end

  # Spawns and supervises the Finesse SSE server from inside Puma.
  #
  # Puma runs plugins in the launcher (master) process only, so in cluster
  # mode the child is spawned once, not per worker. Stop hooks can run in
  # signal-trap context, so stop_child must not use Mutex, Timeout, or
  # anything that spawns a thread; plain ivar access is trap-safe and lost
  # waitpid races surface as ECHILD/ESRCH, which both sides rescue.
  class PumaPlugin
    RESPAWN_DELAY = 1
    TERM_GRACE = 5

    def self.build_config(env = ENV, log: ->(msg) { warn msg })
      PumaPluginConfig.build_config(env, log: log)
    end

    def initialize(launcher)
      @launcher = launcher
      @child_pid = nil
      @stopping = false
    end

    def start
      config = self.class.build_config(log: method(:log))
      return unless config
      return unless (@executable = resolve_executable)

      @config = config
      register_hooks
    end

    private

    def resolve_executable
      Platforms.executable
    rescue Platforms::UnsupportedPlatformError => e
      log "finesse: #{e.message}"
      nil
    end

    def register_hooks
      puma6 = Gem::Version.new(Puma::Const::VERSION) < Gem::Version.new('7')
      # Hook names differ between Puma 6 and 7; see solid_queue's plugin.
      booted, stopped, restart =
        puma6 ? %i[on_booted on_stopped on_restart] : %i[after_booted after_stopped before_restart]
      @launcher.events.public_send(booted) { spawn_and_monitor }
      @launcher.events.public_send(stopped) { stop_child }
      @launcher.events.public_send(restart) { stop_child }
    end

    def spawn_and_monitor
      return if @child_pid

      @stopping = false
      Thread.new { supervise }
    end

    def supervise
      while (pid = spawn_child)
        _, status = Process.waitpid2(pid)
        @child_pid = nil
        break if @stopping

        log "finesse: SSE server pid #{pid} exited unexpectedly (#{status}); restarting in #{RESPAWN_DELAY}s"
        sleep RESPAWN_DELAY
        break if @stopping
      end
    rescue Errno::ECHILD, Errno::ESRCH
      # Child already reaped by stop_child.
    end

    def spawn_child
      pid = Process.spawn(@config[:env], @executable, *@config[:args])
      @child_pid = pid
      log "finesse: SSE server started (pid #{pid})"
      pid
    rescue SystemCallError => e
      log "finesse: failed to start SSE server (#{e.message})"
      nil
    end

    def stop_child
      @stopping = true
      pid = @child_pid
      return unless pid

      log 'finesse: stopping SSE server'
      Process.kill('TERM', pid)
      reap(pid)
    rescue Errno::ECHILD, Errno::ESRCH
      # Already dead or reaped by the supervisor thread.
    end

    # Wait up to TERM_GRACE seconds for the child to exit, then KILL it.
    # Trap-context safe: no Timeout, no threads.
    def reap(pid)
      deadline = Process.clock_gettime(Process::CLOCK_MONOTONIC) + TERM_GRACE
      until Process.clock_gettime(Process::CLOCK_MONOTONIC) > deadline
        return if Process.waitpid(pid, Process::WNOHANG)

        sleep 0.1
      end
      Process.kill('KILL', pid)
      Process.waitpid(pid)
    end

    def log(message)
      @launcher.log_writer.log(message)
    rescue Errno::EIO, Errno::EPIPE, Errno::EBADF
      # Keep shutdown moving even when the log sink is gone.
    end
  end
end
