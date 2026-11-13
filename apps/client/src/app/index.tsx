import { formatRupees, paise } from "@swastik/domain";
import { colors, space } from "@swastik/tokens";
import { StyleSheet, Text, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";

/** Placeholder Home screen; the real Home (greeting, "Do now" card) is built in module M11. */
export default function HomeScreen() {
  return (
    <SafeAreaView style={styles.safe}>
      <View style={styles.container}>
        <Text style={styles.title}>Swastik</Text>
        <Text style={styles.subtitle}>Your IPO tasks, allotments and payments in one place.</Text>
        <Text style={styles.check}>Formatting check: {formatRupees(paise(8658000))}</Text>
      </View>
    </SafeAreaView>
  );
}

const styles = StyleSheet.create({
  safe: { flex: 1, backgroundColor: colors.surface.base },
  container: { flex: 1, padding: space[6], justifyContent: "center", gap: space[3] },
  title: { fontSize: 32, fontWeight: "700", color: colors.brand[600] },
  subtitle: { fontSize: 16, color: colors.ink[600] },
  check: { fontSize: 13, color: colors.ink[400] },
});
