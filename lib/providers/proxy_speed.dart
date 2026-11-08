import 'package:fl_clash/common/common.dart';
import 'package:fl_clash/core/core.dart';
import 'package:fl_clash/models/models.dart';
import 'package:fl_clash/providers/app.dart';
import 'package:fl_clash/providers/core.dart';
import 'package:fl_clash/providers/state.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Same test endpoint and per-node byte budget as the reference implementation.
const int speedTestMaxBytes = 8 * 1024 * 1024;
const int speedTestTimeoutMs = 15000;
const String speedTestUrl =
    'https://speed.cloudflare.com/__down?bytes=$speedTestMaxBytes';

class ProxySpeedEntry {
  const ProxySpeedEntry({
    required this.proxyName,
    required this.mbps,
    required this.bytes,
    required this.durationMs,
    required this.statusCode,
    required this.testUrl,
    required this.testedAt,
  });

  final String proxyName;
  /// Throughput in MiB per second, displayed as MB/s for consistency with UI.
  final double mbps;
  final int bytes;
  final int durationMs;
  final int statusCode;
  final String testUrl;
  final DateTime testedAt;

  String get formatted => '${mbps.toStringAsFixed(1)} MB/s';
}

class SpeedTestingGroups extends Notifier<Set<String>> {
  @override
  Set<String> build() => const {};

  bool start(String groupName) {
    if (state.contains(groupName)) return false;
    state = {...state, groupName};
    return true;
  }

  void stop(String groupName) {
    if (state.contains(groupName)) state = {...state}..remove(groupName);
  }
}

final speedTestingGroupsProvider =
    NotifierProvider<SpeedTestingGroups, Set<String>>(SpeedTestingGroups.new);

class ProxySpeedTest extends Notifier<Map<String, ProxySpeedEntry>> {
  @override
  Map<String, ProxySpeedEntry> build() => const {};

  ProxySpeedEntry? fastestAmong(Iterable<String> proxyNames) {
    ProxySpeedEntry? fastest;
    for (final name in proxyNames) {
      final entry = state[name];
      if (entry != null && (fastest == null || entry.mbps > fastest.mbps)) {
        fastest = entry;
      }
    }
    return fastest;
  }

  Future<ProxySpeedEntry?> test(Proxy proxy) async {
    final name = proxy.name;
    // A failed retest must not leave an old value presented as a new result.
    if (state.containsKey(name)) state = {...state}..remove(name);
    try {
      final realState = computeRealSelectedProxyState(
        name,
        groups: ref.read(groupsProvider),
        selectedMap: ref.read(selectedMapProvider),
      );
      if (realState.proxyName.isEmpty) return null;
      final params = ProbeParams(
        url: speedTestUrl,
        proxyName: realState.proxyName,
        timeout: speedTestTimeoutMs,
        maxBody: speedTestMaxBytes,
      );
      // The typed ProbeResult doesn't yet expose measurement fields. Keep the
      // existing generated model untouched and decode the additive raw fields.
      final data = await ref.read(coreHandlerProvider).invokeMethod(
        method: CoreMethod.probe,
        arguments: {...params.toJson(), 'measure-only': true},
        timeout: probeGuardDuration(params),
      );
      if (!ref.mounted || data is! Map) return null;
      final result = Map<String, dynamic>.from(data);
      final error = result['error'];
      final status = (result['status-code'] as num?)?.toInt() ?? 0;
      final bytes = (result['bytes'] as num?)?.toInt() ?? 0;
      final duration = (result['duration'] as num?)?.toInt() ?? 0;
      if ((error is String && error.isNotEmpty) ||
          status < 200 || status >= 300 || bytes <= 0 || duration <= 0) {
        return null;
      }
      final entry = ProxySpeedEntry(
        proxyName: name,
        mbps: bytes / (1024 * 1024) / (duration / 1000),
        bytes: bytes,
        durationMs: duration,
        statusCode: status,
        testUrl: speedTestUrl,
        testedAt: DateTime.now(),
      );
      state = {...state, name: entry};
      return entry;
    } catch (error, stackTrace) {
      commonPrint.log(
        'Speed test failed for $name: $error\n$stackTrace',
        logLevel: coreFailureLogLevel(error),
      );
      return null;
    }
  }
}

final proxySpeedProvider =
    NotifierProvider<ProxySpeedTest, Map<String, ProxySpeedEntry>>(
      ProxySpeedTest.new,
    );
